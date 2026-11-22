package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/social-media-nlp/refactor/backend/db/sqlc"
	"github.com/social-media-nlp/refactor/backend/internal/config"
	"github.com/social-media-nlp/refactor/backend/internal/notifications"
	"github.com/social-media-nlp/refactor/backend/internal/ws"
)

type socialFixture struct {
	pool  *pgxpool.Pool
	cache *redis.Client
	url   string
	hub   *ws.Hub
}

func newSocialFixture(t *testing.T) socialFixture {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:postgres@127.0.0.1:5432/social_media?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	cacheURL := os.Getenv("VALKEY_URL")
	if cacheURL == "" {
		cacheURL = "redis://127.0.0.1:6379/0"
	}
	opts, err := redis.ParseURL(cacheURL)
	if err != nil {
		t.Fatal(err)
	}
	cache := redis.NewClient(opts)
	t.Cleanup(func() { _ = cache.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{
		JWTSecret: "local-social-access-session-fixture", RefreshJWTSecret: "local-social-refresh-session-fixture",
		JWTTTL: time.Minute, RefreshJWTTTL: 10 * time.Minute,
		LoginLimit: 20, LoginIPLimit: 100, LoginLimitDuration: 10 * time.Second,
		SMTPHost: "127.0.0.1", SMTPPort: 1025,
	}
	handler, hub, err := newApplication(ctx, cfg, pool, cache, logger)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		_ = hub.Shutdown(stopCtx)
		server.Close()
		cancel()
	})
	return socialFixture{pool: pool, cache: cache, url: server.URL, hub: hub}
}

func TestWebSocketDrainSendsReconnectFrame(t *testing.T) {
	fixture := newSocialFixture(t)
	client := fixture.user(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(fixture.url, "http") + "/ws?ticket=" + createTestTicket(t, client)
	socket := openTestSocket(t, ctx, url)
	defer func() { _ = socket.CloseNow() }()
	readTestEvent(t, ctx, socket, "PRESENCE_SNAPSHOT")

	done := make(chan error, 1)
	go func() { done <- fixture.hub.Shutdown(ctx) }()
	for {
		_, _, err := socket.Read(ctx)
		if err == nil {
			continue
		}
		if websocket.CloseStatus(err) != websocket.StatusGoingAway {
			t.Fatalf("expected a clean reconnect frame, got %v", err)
		}
		break
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

type socialClient struct {
	id    uuid.UUID
	token string
	url   string
	http  *http.Client
}

func (f socialFixture) user(t *testing.T, admin bool) socialClient {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	client := socialClient{url: f.url, http: &http.Client{Jar: jar, Timeout: 10 * time.Second}}
	tag := "social-" + uuid.NewString()
	body := map[string]string{"username": tag, "nickName": tag, "email": tag + "@example.test", "password": "fixture-sociaL123"}
	response := client.call(t, http.MethodPost, "/api/auth/register", body, http.StatusCreated)
	var session struct {
		AccessToken string `json:"accessToken"`
		User        struct {
			ID uuid.UUID `json:"id"`
		} `json:"user"`
	}
	decodeSocial(t, response, &session)
	client.id, client.token = session.User.ID, session.AccessToken
	t.Cleanup(func() {
		if _, err := f.pool.Exec(context.Background(), "DELETE FROM users WHERE id = $1", client.id); err != nil {
			t.Error(err)
		}
	})
	if admin {
		if _, err := f.pool.Exec(context.Background(), "UPDATE users SET role = 'ADMIN' WHERE id = $1", client.id); err != nil {
			t.Fatal(err)
		}
		response := client.call(t, http.MethodPost, "/api/auth/login", body, http.StatusOK)
		decodeSocial(t, response, &session)
		client.token = session.AccessToken
	}
	return client
}

func (client socialClient) call(t *testing.T, method, path string, body any, status int) []byte {
	t.Helper()
	data, code, err := client.request(method, path, body)
	if err != nil || code != status {
		t.Fatalf("%s %s: got %d want %d: %s (%v)", method, path, code, status, data, err)
	}
	return data
}

func (client socialClient) request(method, path string, body any) ([]byte, int, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequest(method, client.url+path, reader)
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	if client.token != "" {
		request.Header.Set("Authorization", "Bearer "+client.token)
	}
	response, err := client.http.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	return data, response.StatusCode, err
}

func decodeSocial(t *testing.T, data []byte, target any) {
	t.Helper()
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatalf("decode social response: %v", err)
	}
}

func responseID(t *testing.T, data []byte) string {
	t.Helper()
	var response struct {
		ID string `json:"id"`
	}
	decodeSocial(t, data, &response)
	if response.ID == "" {
		t.Fatalf("response missing id: %s", data)
	}
	return response.ID
}

func TestMilestone2SocialRoutes(t *testing.T) {
	f := newSocialFixture(t)
	owner, visitor, admin := f.user(t, false), f.user(t, false), f.user(t, true)
	groupID := responseID(t, owner.call(t, http.MethodPost, "/api/groups", map[string]string{"name": "Community forum"}, http.StatusCreated))
	group := "/api/groups/" + groupID
	postBody := map[string]string{"content": "Nội dung rất tuyệt " + uuid.NewString()}

	visitor.call(t, http.MethodPost, group+"/posts", postBody, http.StatusForbidden)
	visitor.call(t, http.MethodPost, group+"/join", nil, http.StatusNoContent)
	visitor.call(t, http.MethodPost, group+"/approve/"+visitor.id.String(), nil, http.StatusForbidden)
	owner.call(t, http.MethodPost, group+"/approve/"+visitor.id.String(), nil, http.StatusNoContent)
	postID := responseID(t, visitor.call(t, http.MethodPost, group+"/posts", postBody, http.StatusCreated))
	if body := visitor.call(t, http.MethodGet, "/api/posts", nil, http.StatusOK); bytes.Contains(body, []byte(postID)) {
		t.Fatal("group post leaked into home feed")
	}
	if body := visitor.call(t, http.MethodGet, group+"/posts", nil, http.StatusOK); !bytes.Contains(body, []byte(postID)) {
		t.Fatal("group post missing from group feed")
	}
	visitor.call(t, http.MethodDelete, group+"/membership", nil, http.StatusNoContent)
	visitor.call(t, http.MethodPost, group+"/posts", postBody, http.StatusForbidden)

	mainPost := responseID(t, owner.call(t, http.MethodPost, "/api/posts", map[string]string{"content": "Public post"}, http.StatusCreated))
	comment := responseID(t, visitor.call(t, http.MethodPost, "/api/comments", map[string]string{
		"postId": mainPost, "content": "I love this",
	}, http.StatusCreated))
	visitor.call(t, http.MethodPatch, "/api/comments/"+comment, map[string]string{"content": "This is terrible"}, http.StatusOK)
	owner.call(t, http.MethodDelete, "/api/comments/"+comment, nil, http.StatusForbidden)
	visitor.call(t, http.MethodPost, "/api/posts/"+mainPost+"/like", nil, http.StatusOK)
	path := "/api/bookmarks/" + mainPost
	visitor.call(t, http.MethodPost, path, nil, http.StatusOK)
	bookmarks := visitor.call(t, http.MethodGet, "/api/bookmarks", nil, http.StatusOK)
	var saved struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	decodeSocial(t, bookmarks, &saved)
	if len(saved.Data) != 1 {
		t.Fatalf("expected one bookmark, got %d", len(saved.Data))
	}
	owner.call(t, http.MethodDelete, "/api/bookmarks/"+saved.Data[0].ID, nil, http.StatusForbidden)
	visitor.call(t, http.MethodPost, path, nil, http.StatusOK)
	visitor.call(t, http.MethodDelete, "/api/bookmarks/"+saved.Data[0].ID, nil, http.StatusNoContent)

	notices := owner.call(t, http.MethodGet, "/api/notifications?afterSeq=0", nil, http.StatusOK)
	var noticesPage struct {
		Data []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
			Seq  int64  `json:"userNotifSeq"`
		} `json:"data"`
	}
	decodeSocial(t, notices, &noticesPage)
	var bookmarkCount int
	for i, notice := range noticesPage.Data {
		if notice.Seq != int64(i+1) {
			t.Fatal("notification sequence contains a gap")
		}
		if notice.Type == "BOOKMARK" {
			bookmarkCount++
		}
	}
	if bookmarkCount != 2 {
		t.Fatalf("expected notifications for both bookmark transitions, got %d", bookmarkCount)
	}
	owner.call(t, http.MethodDelete, "/api/notifications/"+noticesPage.Data[0].ID, nil, http.StatusNoContent)
	if body := owner.call(t, http.MethodGet, "/api/notifications?afterSeq=0", nil, http.StatusOK); !bytes.Contains(body, []byte(`"deleted":true`)) {
		t.Fatal("missing notification tombstone")
	}

	ownerProfile := "/api/users/" + owner.id.String()
	visitor.call(t, http.MethodPost, ownerProfile+"/follow", nil, http.StatusOK)
	if body := visitor.call(t, http.MethodGet, ownerProfile, nil, http.StatusOK); !bytes.Contains(body, []byte(`"following":true`)) {
		t.Fatal("profile omitted follow state")
	}
	visitor.call(t, http.MethodGet, "/api/users/recent_birthday?upcoming=true", nil, http.StatusOK)
	visitor.call(t, http.MethodGet, ownerProfile+"/posts", nil, http.StatusOK)

	event := responseID(t, owner.call(t, http.MethodPost, "/api/events", map[string]any{
		"name": "Technology meetup", "description": "Local project demo", "category": "TECHNOLOGY",
		"eventDate": time.Now().UTC().Add(24 * time.Hour),
	}, http.StatusCreated))
	visitor.call(t, http.MethodPost, "/api/events/"+event+"/join", nil, http.StatusNoContent)
	owner.call(t, http.MethodPost, "/api/events/"+event+"/approve/"+visitor.id.String(), nil, http.StatusNoContent)
	visitor.call(t, http.MethodGet, "/api/events/category/TECHNOLOGY", nil, http.StatusOK)

	visitor.call(t, http.MethodGet, "/api/admin/dashboard-stats", nil, http.StatusForbidden)
	admin.call(t, http.MethodGet, "/api/admin/dashboard-stats", nil, http.StatusOK)
	admin.call(t, http.MethodGet, "/api/admin/users", nil, http.StatusOK)
	admin.call(t, http.MethodPatch, "/api/admin/users/"+visitor.id.String()+"/toggle-activity", nil, http.StatusOK)
	visitor.call(t, http.MethodPost, "/api/auth/refresh", nil, http.StatusForbidden)
	admin.call(t, http.MethodDelete, "/api/admin/groups/"+groupID, nil, http.StatusNoContent)
	admin.call(t, http.MethodDelete, "/api/admin/events/"+event, nil, http.StatusNoContent)
}

func TestMilestone2ChatAndTicketRealtime(t *testing.T) {
	f := newSocialFixture(t)
	sender, recipient, outside := f.user(t, false), f.user(t, false), f.user(t, false)
	room := responseID(t, sender.call(t, http.MethodPost, "/api/chat/rooms", map[string]any{
		"type": "DIRECT", "participantIds": []uuid.UUID{recipient.id},
	}, http.StatusCreated))
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(), "DELETE FROM chat_rooms WHERE id = $1", room)
	})
	roomMessages := "/api/chat/rooms/" + room + "/messages"
	recipient.call(t, http.MethodGet, "/api/chat/rooms/sync", nil, http.StatusOK)
	outside.call(t, http.MethodGet, roomMessages, nil, http.StatusForbidden)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(f.url, "http") + "/ws"
	expiredTicket := createTestTicket(t, recipient)
	if err := f.cache.Expire(ctx, "ws_ticket:"+expiredTicket, 0).Err(); err != nil {
		t.Fatal(err)
	}
	assertTicketDenied(t, ctx, wsURL+"?ticket="+expiredTicket)
	assertTicketDenied(t, ctx, wsURL)
	firstTicket := createTestTicket(t, recipient)
	first := openTestSocket(t, ctx, wsURL+"?ticket="+firstTicket)
	defer first.CloseNow()
	assertTicketDenied(t, ctx, wsURL+"?ticket="+firstTicket)
	second := openTestSocket(t, ctx, wsURL+"?ticket="+createTestTicket(t, recipient))
	defer second.CloseNow()
	readTestEvent(t, ctx, first, "PRESENCE_SNAPSHOT")
	readTestEvent(t, ctx, second, "PRESENCE_SNAPSHOT")

	const messages = 5
	var group sync.WaitGroup
	errs := make(chan error, messages)
	for i := range messages {
		group.Go(func() {
			body := map[string]string{"content": fmt.Sprintf("Message %d https://example.test/shared", i), "senderId": outside.id.String()}
			data, status, err := sender.request(http.MethodPost, roomMessages, body)
			if err != nil || status != http.StatusCreated {
				errs <- fmt.Errorf("send status %d: %s (%v)", status, data, err)
			}
		})
	}
	group.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	firstLive := readTestEvent(t, ctx, first, "NEW_MESSAGE")
	secondLive := readTestEvent(t, ctx, second, "NEW_MESSAGE")
	if !bytes.Equal(firstLive, secondLive) {
		t.Fatal("presence tabs received different first room message")
	}
	_ = first.Close(websocket.StatusNormalClosure, "tab closed")
	presence, err := f.cache.ZCard(ctx, "{ws:presence}:user:"+recipient.id.String()).Result()
	if err != nil || presence < 1 {
		t.Fatalf("remaining tab lost presence: %d, %v", presence, err)
	}
	if body := recipient.call(t, http.MethodGet, "/api/chat/rooms/sync", nil, http.StatusOK); !bytes.Contains(body, []byte(`"lastSeq":5`)) {
		t.Fatalf("room summary did not recover last sequence: %s", body)
	}
	var page struct {
		Data []struct {
			RoomSeq int64     `json:"roomSeq"`
			Sender  uuid.UUID `json:"senderId"`
		} `json:"data"`
	}
	decodeSocial(t, recipient.call(t, http.MethodGet, roomMessages+"?afterSeq=0", nil, http.StatusOK), &page)
	if len(page.Data) != messages {
		t.Fatalf("expected %d catch-up messages, got %d", messages, len(page.Data))
	}
	for i, message := range page.Data {
		if message.RoomSeq != int64(i+1) || message.Sender != sender.id {
			t.Fatalf("unexpected catch-up message: %+v", message)
		}
	}
	recipient.call(t, http.MethodGet, "/api/chat/rooms/"+room+"/shared", nil, http.StatusOK)
}

func TestMilestone2SequenceAllocationSerializesCommits(t *testing.T) {
	f := newSocialFixture(t)
	sender, receiver := f.user(t, false), f.user(t, false)
	room := responseID(t, sender.call(t, http.MethodPost, "/api/chat/rooms", map[string]any{
		"type": "DIRECT", "participantIds": []uuid.UUID{receiver.id},
	}, http.StatusCreated))
	roomID := uuid.MustParse(room)
	t.Cleanup(func() { _, _ = f.pool.Exec(context.Background(), "DELETE FROM chat_rooms WHERE id = $1", roomID) })
	queries := sqlc.New(f.pool)
	t.Run("room", func(t *testing.T) {
		verifySequenceCommitOrder(t, f.pool, func(ctx context.Context, q *sqlc.Queries) (int64, error) {
			seq, err := q.AdvanceChatRoomSequence(ctx, sqlc.AdvanceChatRoomSequenceParams{RoomID: roomID, UserID: sender.id})
			if err != nil {
				return 0, err
			}
			_, err = q.CreateChatMessage(ctx, sqlc.CreateChatMessageParams{
				ChatRoomID: roomID, SenderID: pgtype.UUID{Bytes: sender.id, Valid: true}, RoomSeq: seq, Content: "ordered",
			})
			return seq, err
		}, func(ctx context.Context) ([]int64, error) {
			rows, err := queries.ListChatMessagesAfterSeq(ctx, sqlc.ListChatMessagesAfterSeqParams{RoomID: roomID, PageLimit: 10})
			sequences := make([]int64, 0, len(rows))
			for _, row := range rows {
				sequences = append(sequences, row.RoomSeq)
			}
			return sequences, err
		})
	})
	t.Run("notifications", func(t *testing.T) {
		verifySequenceCommitOrder(t, f.pool, func(ctx context.Context, q *sqlc.Queries) (int64, error) {
			row, err := notifications.Create(ctx, q, sender.id, receiver.id, uuid.Nil, "LIKE", "ordered")
			return row.UserNotifSeq, err
		}, func(ctx context.Context) ([]int64, error) {
			rows, err := queries.ListNotificationsAfterSeq(ctx, sqlc.ListNotificationsAfterSeqParams{ReceiverID: receiver.id, PageLimit: 10})
			sequences := make([]int64, 0, len(rows))
			for _, row := range rows {
				sequences = append(sequences, row.UserNotifSeq)
			}
			return sequences, err
		})
	})
}

func verifySequenceCommitOrder(t *testing.T, pool *pgxpool.Pool,
	write func(context.Context, *sqlc.Queries) (int64, error),
	read func(context.Context) ([]int64, error),
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Rollback(ctx)
	seq, err := write(ctx, sqlc.New(first))
	if err != nil || seq != 1 {
		t.Fatalf("first sequence = %d, error = %v", seq, err)
	}
	attempting := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- pgx.BeginFunc(ctx, pool, func(second pgx.Tx) error {
			close(attempting)
			seq, err := write(ctx, sqlc.New(second))
			if err == nil && seq != 2 {
				err = fmt.Errorf("second sequence = %d, want 2", seq)
			}
			return err
		})
	}()
	select {
	case <-attempting:
	case err := <-done:
		t.Fatalf("second transaction could not start: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case err := <-done:
		t.Fatalf("second transaction completed before the first committed: %v", err)
	case <-time.After(75 * time.Millisecond):
	}
	sequences, err := read(ctx)
	if err != nil || len(sequences) != 0 {
		t.Fatalf("uncommitted sequences visible: %v, error = %v", sequences, err)
	}
	if err := first.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	sequences, err = read(ctx)
	if err != nil || len(sequences) != 2 || sequences[0] != 1 || sequences[1] != 2 {
		t.Fatalf("expected ordered committed sequences [1 2], got %v, error = %v", sequences, err)
	}
}

func createTestTicket(t *testing.T, client socialClient) string {
	t.Helper()
	var response struct {
		Ticket string `json:"ticket"`
	}
	decodeSocial(t, client.call(t, http.MethodPost, "/api/auth/ws-ticket", nil, http.StatusCreated), &response)
	return response.Ticket
}

func openTestSocket(t *testing.T, ctx context.Context, url string) *websocket.Conn {
	t.Helper()
	socket, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return socket
}

func assertTicketDenied(t *testing.T, ctx context.Context, url string) {
	t.Helper()
	socket, response, err := websocket.Dial(ctx, url, nil)
	if socket != nil {
		socket.CloseNow()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected websocket HTTP 401, got %v", err)
	}
}

func readTestEvent(t *testing.T, ctx context.Context, conn *websocket.Conn, kind string) []byte {
	t.Helper()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read %s: %v", kind, err)
		}
		var event struct {
			Type string `json:"type"`
		}
		decodeSocial(t, data, &event)
		if event.Type == kind {
			return data
		}
	}
}
