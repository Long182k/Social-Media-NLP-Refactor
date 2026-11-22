package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/social-media-nlp/refactor/backend/db/sqlc"
	"github.com/social-media-nlp/refactor/backend/internal/nlp/lexicon"
	"github.com/social-media-nlp/refactor/backend/internal/realtime"
)

const (
	concurrency   = 4
	queueCapacity = 256
)

type EntityType string

const (
	Post    EntityType = "post"
	Comment EntityType = "comment"
)

type Job struct {
	EntityType EntityType
	ID         uuid.UUID
	Version    int32
	Content    string
}

type classifier interface {
	Infer(context.Context, string) (lexicon.Sentiment, error)
}

type Pool struct {
	ctx       context.Context
	pending   chan Job
	inference classifier
	queries   *sqlc.Queries
	publisher realtime.Publisher
	dropped   prometheus.Counter
}

func Start(ctx context.Context, client classifier, queries *sqlc.Queries, publisher realtime.Publisher, dropped prometheus.Counter) *Pool {
	pool := &Pool{
		ctx: ctx, pending: make(chan Job, queueCapacity), inference: client,
		queries: queries, publisher: publisher, dropped: dropped,
	}
	for range concurrency {
		go pool.run()
	}
	return pool
}

func (p *Pool) Offer(job Job) bool {
	if p == nil || job.Content == "" || p.ctx.Err() != nil {
		return false
	}
	select {
	case p.pending <- job:
		return true
	default:
		p.dropped.Inc()
		return false
	}
}

func (p *Pool) run() {
	for p.ctx.Err() == nil {
		select {
		case <-p.ctx.Done():
			return
		case job := <-p.pending:
			p.refine(job)
		}
	}
}

func (p *Pool) refine(job Job) {
	label, err := p.inference.Infer(p.ctx, job.Content)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(p.ctx, 2*time.Second)
	defer cancel()
	var kind string
	var affected int64
	switch job.EntityType {
	case Post:
		kind = "POST_SENTIMENT_UPDATED"
		affected, err = p.queries.UpdatePostSentimentIfCurrent(ctx, sqlc.UpdatePostSentimentIfCurrentParams{
			ID: job.ID, Sentiment: sqlc.SentimentType(label), ContentVersion: job.Version,
		})
	case Comment:
		kind = "COMMENT_SENTIMENT_UPDATED"
		affected, err = p.queries.UpdateCommentSentimentIfCurrent(ctx, sqlc.UpdateCommentSentimentIfCurrentParams{
			ID: job.ID, Sentiment: sqlc.SentimentType(label), ContentVersion: job.Version,
		})
	default:
		return
	}
	if err != nil {
		p.publisher.Logger.Warn("sentiment update failed", slog.String("entityType", string(job.EntityType)), slog.String("error", err.Error()))
		return
	}
	if affected == 1 {
		p.publisher.PublishSentiment(ctx, kind, realtime.SentimentUpdate{
			EntityType: string(job.EntityType), EntityID: job.ID, Sentiment: string(label), ContentVersion: job.Version,
		})
	}
}
