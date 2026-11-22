package worker

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

var ErrBudgetUnavailable = errors.New("daily Workers AI budget is exhausted or unavailable")

var reserveNeurons = redis.NewScript(`
local t = redis.call('TIME')
local now = tonumber(t[1])
if now < tonumber(ARGV[1]) or now >= tonumber(ARGV[2]) - 8 then return 0 end
local raw = redis.call('GET', KEYS[1])
local used = tonumber(raw)
local amount = tonumber(ARGV[3])
if not used or used < 0 or used ~= math.floor(used) or redis.call('EXPIRETIME', KEYS[1]) ~= tonumber(ARGV[2]) + 3600 then return 0 end
if amount < 1 or used + amount > 9000 then return 0 end
redis.call('INCRBY', KEYS[1], amount)
return 1
`)

func dayWindow(now time.Time) (string, time.Time, time.Time) {
	now = now.UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return "cf_ai:neurons:" + today.Format("2006-01-02"), today, today.AddDate(0, 0, 1)
}

func reserve(ctx context.Context, cache *redis.Client, now time.Time, amount int64) (string, error) {
	key, start, end := dayWindow(now)
	allowed, err := reserveNeurons.Run(ctx, cache, []string{key}, start.Unix(), end.Unix(), amount).Int()
	if err != nil || allowed != 1 {
		return "", ErrBudgetUnavailable
	}
	return key, nil
}

// InitializeBudget is an explicit setup operation using a recent dashboard
// reading after the account has switched all consumers to the shared ledger.
func InitializeBudget(ctx context.Context, cache *redis.Client, evidence Evidence, now time.Time) error {
	if err := evidence.verify(now); err != nil {
		return err
	}
	key, start, end := dayWindow(now)
	age := now.Sub(evidence.UsageObservedAt)
	if age < 0 || age > 5*time.Minute || evidence.UsageObservedAt.Before(start) ||
		evidence.NeuronsAlreadyUsed < 0 || evidence.NeuronsAlreadyUsed > DailyLimit {
		return ErrBudgetUnavailable
	}
	created, err := cache.SetArgs(ctx, key, strconv.Itoa(evidence.NeuronsAlreadyUsed), redis.SetArgs{Mode: "NX", ExpireAt: end.Add(time.Hour)}).Result()
	if err != nil || created != "OK" {
		return ErrBudgetUnavailable
	}
	return nil
}
