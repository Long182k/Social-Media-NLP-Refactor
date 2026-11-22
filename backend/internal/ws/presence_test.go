package ws

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func TestPresenceAcrossReplicasRetainsRemainingTab(t *testing.T) {
	url := os.Getenv("VALKEY_URL")
	if url == "" {
		url = "redis://127.0.0.1:6379/0"
	}
	options, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	firstCache, secondCache := redis.NewClient(options), redis.NewClient(options)
	defer firstCache.Close()
	defer secondCache.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	firstHub := &Hub{cache: firstCache, logger: logger}
	secondHub := &Hub{cache: secondCache, logger: logger}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	userID := uuid.New()
	first := &connection{id: uuid.New(), userID: userID}
	second := &connection{id: uuid.New(), userID: userID}
	defer firstHub.updatePresence(context.Background(), first, false)
	defer secondHub.updatePresence(context.Background(), second, false)
	subscription := secondCache.Subscribe(ctx, presenceChannel)
	defer subscription.Close()
	if _, err := subscription.Receive(ctx); err != nil {
		t.Fatal(err)
	}
	firstHub.updatePresence(ctx, first, true)
	assertPresenceEvent(t, ctx, subscription, userID, true)
	secondHub.updatePresence(ctx, second, true)
	firstHub.updatePresence(ctx, first, false)
	if count, err := secondCache.ZCard(ctx, "{ws:presence}:user:"+userID.String()).Result(); err != nil || count != 1 {
		t.Fatalf("remaining connection count = %d, error = %v", count, err)
	}
	secondHub.updatePresence(ctx, second, false)
	assertPresenceEvent(t, ctx, subscription, userID, false)
}

func assertPresenceEvent(t *testing.T, ctx context.Context, subscription *redis.PubSub, userID uuid.UUID, online bool) {
	t.Helper()
	for {
		message, err := subscription.ReceiveMessage(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var event struct {
			Payload struct {
				UserID uuid.UUID `json:"userId"`
				Online bool      `json:"online"`
			} `json:"payload"`
		}
		if err := json.Unmarshal([]byte(message.Payload), &event); err != nil {
			t.Fatal(err)
		}
		if event.Payload.UserID == userID {
			if event.Payload.Online != online {
				t.Fatalf("presence online = %t, want %t", event.Payload.Online, online)
			}
			return
		}
	}
}
