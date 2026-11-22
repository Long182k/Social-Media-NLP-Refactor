package realtime

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func UserChannel(id uuid.UUID) string {
	return "ws:user:" + id.String()
}

type Publisher struct {
	Client *redis.Client
	Logger *slog.Logger
}

const SentimentChannel = "ws:sentiment"

type SentimentUpdate struct {
	EntityType     string    `json:"entityType"`
	EntityID       uuid.UUID `json:"entityId"`
	Sentiment      string    `json:"sentiment"`
	ContentVersion int32     `json:"contentVersion"`
}

type envelope struct {
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

func (p Publisher) PublishSentiment(ctx context.Context, kind string, payload SentimentUpdate) {
	event, err := json.Marshal(envelope{kind, payload})
	if err == nil {
		err = p.Client.Publish(ctx, SentimentChannel, event).Err()
	}
	if err != nil {
		p.Logger.Warn("sentiment publish failed", slog.String("type", kind), slog.String("error", err.Error()))
	}
}

func (p Publisher) Publish(ctx context.Context, recipients []uuid.UUID, kind string, payload any) {
	if len(recipients) == 0 {
		return
	}
	event, err := json.Marshal(envelope{kind, payload})
	if err == nil {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_, err = p.Client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
			for _, id := range recipients {
				pipe.Publish(ctx, UserChannel(id), event)
			}
			return nil
		})
	}
	if err != nil {
		p.Logger.Warn("realtime publish failed", slog.String("type", kind), slog.String("error", err.Error()))
	}
}
