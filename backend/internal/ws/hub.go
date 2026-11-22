package ws

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/social-media-nlp/refactor/backend/db/sqlc"
	"github.com/social-media-nlp/refactor/backend/internal/auth"
	"github.com/social-media-nlp/refactor/backend/internal/httpserver"
	"github.com/social-media-nlp/refactor/backend/internal/realtime"
)

const (
	ticketTTL       = 30 * time.Second
	presenceChannel = "ws:presence"
)

type Hub struct {
	cache          *redis.Client
	users          *sqlc.Queries
	auth           *auth.Service
	logger         *slog.Logger
	origins        map[string]bool
	originPatterns []string

	lock    sync.Mutex
	closing bool
	clients map[uuid.UUID]map[*connection]struct{}
	active  sync.WaitGroup
}

type connection struct {
	id        uuid.UUID
	userID    uuid.UUID
	drain     chan struct{}
	drainOnce sync.Once
	send      chan []byte
}

func (client *connection) requestDrain() {
	client.drainOnce.Do(func() { close(client.drain) })
}

func NewHub(pool *pgxpool.Pool, cache *redis.Client, authService *auth.Service, logger *slog.Logger, origins []string) *Hub {
	h := &Hub{
		cache: cache, users: sqlc.New(pool), auth: authService, logger: logger,
		origins: map[string]bool{}, clients: map[uuid.UUID]map[*connection]struct{}{},
	}
	for _, raw := range origins {
		origin, err := url.Parse(raw)
		if err == nil && origin.Host != "" && (origin.Scheme == "http" || origin.Scheme == "https") {
			h.origins[strings.ToLower(origin.Scheme+"://"+origin.Host)] = true
			h.originPatterns = append(h.originPatterns, origin.Scheme+"://"+origin.Host)
		}
	}
	return h
}

func (h *Hub) RegisterAPIRoutes(r chi.Router) {
	r.With(auth.RequireAuth(h.auth)).Post("/auth/ws-ticket", h.issueTicket)
}

func (h *Hub) issueTicket(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	active, err := h.isActive(r.Context(), claims.UserID)
	if err != nil {
		h.unavailable(w, err)
		return
	}
	if !active {
		httpserver.WriteError(w, http.StatusForbidden, "FORBIDDEN", "account inactive")
		return
	}
	var nonce [32]byte
	rand.Read(nonce[:])
	ticket := base64.RawURLEncoding.EncodeToString(nonce[:])
	if err := h.cache.Set(r.Context(), ticketKey(ticket), claims.UserID.String(), ticketTTL).Err(); err != nil {
		h.unavailable(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpserver.WriteJSON(w, http.StatusCreated, map[string]any{"ticket": ticket, "expiresIn": ticketTTL.Seconds()})
}

func ticketKey(ticket string) string {
	return "ws_ticket:" + ticket
}

func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.lock.Lock()
	closing := h.closing
	h.lock.Unlock()
	if closing {
		httpserver.WriteError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "realtime server restarting")
		return
	}
	if !h.originAllowed(r) {
		httpserver.WriteError(w, http.StatusForbidden, "FORBIDDEN", "untrusted websocket origin")
		return
	}
	ticket := r.URL.Query().Get("ticket")
	if len(ticket) != base64.RawURLEncoding.EncodedLen(32) {
		invalidTicket(w)
		return
	}
	if _, err := base64.RawURLEncoding.DecodeString(ticket); err != nil {
		invalidTicket(w)
		return
	}
	value, err := h.cache.GetDel(r.Context(), ticketKey(ticket)).Result()
	if errors.Is(err, redis.Nil) {
		invalidTicket(w)
		return
	}
	if err != nil {
		h.unavailable(w, err)
		return
	}
	userID, err := uuid.Parse(value)
	if err != nil {
		invalidTicket(w)
		return
	}
	active, err := h.isActive(r.Context(), userID)
	if err != nil {
		h.unavailable(w, err)
		return
	}
	if !active {
		invalidTicket(w)
		return
	}
	socket, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: h.originPatterns})
	if err != nil {
		return
	}
	defer socket.CloseNow()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &connection{id: uuid.New(), userID: userID, drain: make(chan struct{}), send: make(chan []byte, 64)}
	if !h.add(client) {
		_ = socket.Close(websocket.StatusGoingAway, "server restarting")
		return
	}
	defer h.remove(client)
	h.updatePresence(ctx, client, true)

	socket.SetReadLimit(4096)
	readCtx := socket.CloseRead(ctx)
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	if err := h.sendSnapshot(ctx, socket); err != nil {
		return
	}
	for {
		select {
		case <-client.drain:
			_ = socket.Close(websocket.StatusGoingAway, "reconnect")
			return
		case <-readCtx.Done():
			return
		case body := <-client.send:
			if err := writeMessage(ctx, socket, body); err != nil {
				return
			}
		case <-ticker.C:
			pingCtx, stopPing := context.WithTimeout(ctx, 5*time.Second)
			err := socket.Ping(pingCtx)
			stopPing()
			if err != nil {
				return
			}
			h.updatePresence(ctx, client, true)
			if err := h.sendSnapshot(ctx, socket); err != nil {
				return
			}
		}
	}
}

func (h *Hub) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host) || h.origins[strings.ToLower(origin)]
}

func (h *Hub) isActive(ctx context.Context, id uuid.UUID) (bool, error) {
	user, err := h.users.GetPublicUserByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return user.IsActive, err
}

func (h *Hub) add(client *connection) bool {
	h.lock.Lock()
	defer h.lock.Unlock()
	if h.closing {
		return false
	}
	if h.clients[client.userID] == nil {
		h.clients[client.userID] = map[*connection]struct{}{}
	}
	h.clients[client.userID][client] = struct{}{}
	h.active.Add(1)
	return true
}

func (h *Hub) remove(client *connection) {
	h.lock.Lock()
	delete(h.clients[client.userID], client)
	if len(h.clients[client.userID]) == 0 {
		delete(h.clients, client.userID)
	}
	h.lock.Unlock()
	h.updatePresence(context.Background(), client, false)
	h.active.Done()
}

func (h *Hub) Run(ctx context.Context) {
	subscription := h.cache.PSubscribe(ctx, "ws:user:*", presenceChannel, realtime.SentimentChannel)
	defer subscription.Close()
	events := subscription.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			h.dispatch(event)
		}
	}
}

func (h *Hub) dispatch(event *redis.Message) {
	h.lock.Lock()
	defer h.lock.Unlock()
	if event.Channel == presenceChannel || event.Channel == realtime.SentimentChannel {
		for _, clients := range h.clients {
			h.enqueue(clients, event.Payload)
		}
		return
	}
	id, err := uuid.Parse(strings.TrimPrefix(event.Channel, "ws:user:"))
	if err != nil {
		return
	}
	var envelope struct {
		Type string `json:"type"`
	}
	if json.Unmarshal([]byte(event.Payload), &envelope) == nil && envelope.Type == "USER_DEACTIVATED" {
		for client := range h.clients[id] {
			client.requestDrain()
		}
		return
	}
	h.enqueue(h.clients[id], event.Payload)
}

func (h *Hub) enqueue(clients map[*connection]struct{}, payload string) {
	for client := range clients {
		select {
		case client.send <- []byte(payload):
		default:
			client.requestDrain()
		}
	}
}

func (h *Hub) StopAccepting() {
	h.lock.Lock()
	h.closing = true
	h.lock.Unlock()
}

func (h *Hub) ConnectionCount() float64 {
	h.lock.Lock()
	defer h.lock.Unlock()
	count := 0
	for _, clients := range h.clients {
		count += len(clients)
	}
	return float64(count)
}

func (h *Hub) Shutdown(ctx context.Context) error {
	h.StopAccepting()
	h.lock.Lock()
	for _, clients := range h.clients {
		for client := range clients {
			client.requestDrain()
		}
	}
	h.lock.Unlock()
	done := make(chan struct{})
	go func() {
		h.active.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func (h *Hub) unavailable(w http.ResponseWriter, err error) {
	h.logger.Warn("websocket ticket service unavailable", slog.String("error", err.Error()))
	httpserver.WriteError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "realtime service unavailable")
}

func invalidTicket(w http.ResponseWriter) {
	httpserver.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing, expired, or already used websocket ticket")
}

func writeMessage(ctx context.Context, socket *websocket.Conn, data []byte) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return socket.Write(ctx, websocket.MessageText, data)
}
