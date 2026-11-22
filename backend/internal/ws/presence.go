package ws

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/redis/go-redis/v9"
)

const presenceIndex = "{ws:presence}:users"

var presenceUpdate = redis.NewScript(`
local stamp = redis.call('TIME')
local now = stamp[1] * 1000 + math.floor(stamp[2] / 1000)
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now)
redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', now)
local before = redis.call('ZCARD', KEYS[1])
if ARGV[3] == '1' then
    redis.call('ZADD', KEYS[1], now + 75000, ARGV[2])
    redis.call('PEXPIRE', KEYS[1], 75000)
else
    redis.call('ZREM', KEYS[1], ARGV[2])
end
local latest = redis.call('ZREVRANGE', KEYS[1], 0, 0, 'WITHSCORES')
if #latest == 0 then
    redis.call('ZREM', KEYS[2], ARGV[1])
    if before > 0 then return -1 end
else
    redis.call('ZADD', KEYS[2], latest[2], ARGV[1])
    if before == 0 then return 1 end
end
return 0
`)

func (h *Hub) updatePresence(ctx context.Context, client *connection, online bool) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	userID := client.userID.String()
	flag := 0
	if online {
		flag = 1
	}
	transition, err := presenceUpdate.Run(ctx, h.cache,
		[]string{"{ws:presence}:user:" + userID, presenceIndex}, userID, client.id.String(), flag).Int()
	if err != nil {
		h.logger.Warn("presence update failed", slog.String("error", err.Error()))
		return
	}
	if transition != 0 {
		event, _ := json.Marshal(map[string]any{
			"type": "PRESENCE_UPDATE", "payload": map[string]any{"userId": userID, "online": transition == 1},
		})
		if err := h.cache.Publish(ctx, presenceChannel, event).Err(); err != nil {
			h.logger.Warn("presence event failed", slog.String("error", err.Error()))
		}
	}
}

func (h *Hub) sendSnapshot(ctx context.Context, socket *websocket.Conn) error {
	query, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	now, err := h.cache.Time(query).Result()
	if err != nil {
		return err
	}
	users, err := h.cache.ZRangeArgs(query, redis.ZRangeArgs{
		Key: presenceIndex, Start: "(" + strconv.FormatInt(now.UnixMilli(), 10), Stop: "+inf", ByScore: true, Count: 500,
	}).Result()
	if err != nil {
		return err
	}
	data, _ := json.Marshal(map[string]any{"type": "PRESENCE_SNAPSHOT", "payload": users})
	return writeMessage(ctx, socket, data)
}
