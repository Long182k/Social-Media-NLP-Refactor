package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type rotateOutcome string

const (
	rotateOK      rotateOutcome = "OK"
	rotateMissing rotateOutcome = "MISSING"
	rotateReuse   rotateOutcome = "REUSE"
)

var (
	acquireLoginAttemptLua = redis.NewScript(`
local acctKey = KEYS[1]
local ipKey = KEYS[2]
local acctLimit = tonumber(ARGV[1])
local ipLimit = tonumber(ARGV[2])
local window = tonumber(ARGV[3])

local acctCount = tonumber(redis.call("GET", acctKey) or "0")
if acctCount >= acctLimit then
  local ttl = redis.call("TTL", acctKey)
  if ttl < 1 then ttl = 1 end
  return {1, ttl}
end

local ipCount = tonumber(redis.call("GET", ipKey) or "0")
if ipCount >= ipLimit then
  local ttl = redis.call("TTL", ipKey)
  if ttl < 1 then ttl = 1 end
  return {1, ttl}
end

local newAcct = redis.call("INCR", acctKey)
if newAcct == 1 then
  redis.call("EXPIRE", acctKey, window)
end

local newIP = redis.call("INCR", ipKey)
if newIP == 1 then
  redis.call("EXPIRE", ipKey, window)
end

return {0, 0}
`)

	releaseLoginSuccessLua = redis.NewScript(`
redis.call("DEL", KEYS[1])
if redis.call("EXISTS", KEYS[2]) == 1 then
  local v = redis.call("DECR", KEYS[2])
  if v <= 0 then
    redis.call("DEL", KEYS[2])
  end
end
return 1
`)

	refundLoginAttemptLua = redis.NewScript(`
for _, k in ipairs(KEYS) do
  if redis.call("EXISTS", k) == 1 then
    local v = redis.call("DECR", k)
    if v <= 0 then
      redis.call("DEL", k)
    end
  end
end
return 1
`)

	createSessionLua = redis.NewScript(`
redis.call("SET", KEYS[1], ARGV[1], "EX", tonumber(ARGV[2]))
redis.call("SADD", KEYS[2], ARGV[3])
redis.call("EXPIRE", KEYS[2], tonumber(ARGV[2]))
return 1
`)

	peekSessionLua = redis.NewScript(`
local val = redis.call("GET", KEYS[1])
if not val then
  return "MISSING"
end
local expectedPrefix = ARGV[1] .. ":" .. ARGV[2] .. ":" .. ARGV[3] .. ":"
if string.sub(val, 1, #expectedPrefix) ~= expectedPrefix then
  redis.call("DEL", KEYS[1])
  redis.call("SREM", KEYS[2], ARGV[4])
  return "REUSE"
end
return "OK"
`)

	// Value format: "<userID>:<currentJTI>:<epoch>:<prevJTI>"
	// If val already matches "<userID>:<newJTI>:<epoch>:<presentedJTI>", a retry of the exact
	// same rotation (e.g. after a dropped Redis reply) is treated idempotently as OK.
	rotateSessionLua = redis.NewScript(`
local val = redis.call("GET", KEYS[1])
if not val then
  return "MISSING"
end
local expectedPrefix = ARGV[1] .. ":" .. ARGV[2] .. ":" .. ARGV[7] .. ":"
local rotatedExact = ARGV[1] .. ":" .. ARGV[3] .. ":" .. ARGV[7] .. ":" .. ARGV[2]
if val == rotatedExact then
  redis.call("EXPIRE", KEYS[1], tonumber(ARGV[4]))
  redis.call("SADD", KEYS[2], ARGV[5])
  redis.call("EXPIRE", KEYS[2], tonumber(ARGV[4]))
  return "OK"
end
if string.sub(val, 1, #expectedPrefix) ~= expectedPrefix then
  redis.call("DEL", KEYS[1])
  redis.call("SREM", KEYS[2], ARGV[5])
  return "REUSE"
end
redis.call("SET", KEYS[1], rotatedExact, "EX", tonumber(ARGV[4]))
redis.call("SADD", KEYS[2], ARGV[5])
redis.call("EXPIRE", KEYS[2], tonumber(ARGV[4]))
return "OK"
`)

	revokeSessionLua = redis.NewScript(`
redis.call("DEL", KEYS[1])
redis.call("SREM", KEYS[2], ARGV[1])
return 1
`)

	revokeAllUserSessionsLua = redis.NewScript(`
local fams = redis.call("SMEMBERS", KEYS[1])
for _, fam in ipairs(fams) do
  redis.call("DEL", "refresh:family:" .. fam)
end
redis.call("DEL", KEYS[1])
return #fams
`)

	// Revoke only families whose stored session epoch is strictly less than minValidEpoch,
	// preserving concurrent logins created under the new post-reset epoch.
	revokeUserSessionsBeforeEpochLua = redis.NewScript(`
local minEpoch = tonumber(ARGV[1])
local fams = redis.call("SMEMBERS", KEYS[1])
local revoked = 0
for _, fam in ipairs(fams) do
  local fKey = "refresh:family:" .. fam
  local val = redis.call("GET", fKey)
  if not val then
    redis.call("SREM", KEYS[1], fam)
  else
    local epStr = string.match(val, "^[^:]+:[^:]+:([0-9]+):")
    local ep = tonumber(epStr or "0")
    if ep < minEpoch then
      redis.call("DEL", fKey)
      redis.call("SREM", KEYS[1], fam)
      revoked = revoked + 1
    end
  end
end
return revoked
`)
)

type valkeyStore struct {
	rdb *redis.Client
}

func newValkeyStore(rdb *redis.Client) *valkeyStore {
	return &valkeyStore{rdb: rdb}
}

func loginAttemptAccountKey(userID uuid.UUID) string {
	return "login_attempts:acct:" + userID.String()
}

func loginAttemptIdentKey(normID string) string {
	return "login_attempts:ident:" + normID
}

func loginAttemptIPKey(normIP string) string {
	return "login_attempts:ip:" + normIP
}

func sessionFamilyKey(famID string) string {
	return "refresh:family:" + famID
}

func userSessionsKey(userID uuid.UUID) string {
	return "refresh:user:" + userID.String()
}

func (v *valkeyStore) AcquireLoginAttempt(ctx context.Context, acctKey, ipKey string, acctLimit, ipLimit int, window time.Duration) (locked bool, retryAfter int, err error) {
	sec := int64(window.Seconds())
	if sec < 1 {
		sec = 1
	}
	res, err := acquireLoginAttemptLua.Run(ctx, v.rdb, []string{acctKey, ipKey}, acctLimit, ipLimit, sec).Int64Slice()
	if err != nil {
		return false, 0, err
	}
	if len(res) >= 2 && res[0] == 1 {
		ttl := int(res[1])
		if ttl < 1 {
			ttl = 1
		}
		return true, ttl, nil
	}
	return false, 0, nil
}

func (v *valkeyStore) ReleaseLoginSuccess(ctx context.Context, acctKey, ipKey string) error {
	return releaseLoginSuccessLua.Run(ctx, v.rdb, []string{acctKey, ipKey}).Err()
}

func (v *valkeyStore) RefundLoginAttempt(ctx context.Context, acctKey, ipKey string) error {
	return refundLoginAttemptLua.Run(ctx, v.rdb, []string{acctKey, ipKey}).Err()
}

func (v *valkeyStore) CreateSession(ctx context.Context, userID uuid.UUID, famID, jti string, epoch int64, ttl time.Duration) error {
	sec := int64(ttl.Seconds())
	if sec < 1 {
		sec = 1
	}
	val := fmt.Sprintf("%s:%s:%d:", userID.String(), jti, epoch)
	return createSessionLua.Run(ctx, v.rdb, []string{sessionFamilyKey(famID), userSessionsKey(userID)}, val, sec, famID).Err()
}

func (v *valkeyStore) PeekSession(ctx context.Context, userID uuid.UUID, famID, presentedJTI string, epoch int64) (rotateOutcome, error) {
	res, err := peekSessionLua.Run(
		ctx,
		v.rdb,
		[]string{sessionFamilyKey(famID), userSessionsKey(userID)},
		userID.String(),
		presentedJTI,
		epoch,
		famID,
	).Text()
	if err != nil {
		return "", err
	}
	return rotateOutcome(res), nil
}

func (v *valkeyStore) RotateSession(ctx context.Context, userID uuid.UUID, famID, presentedJTI, newJTI string, epoch int64, ttl time.Duration) (rotateOutcome, error) {
	sec := int64(ttl.Seconds())
	if sec < 1 {
		sec = 1
	}
	res, err := rotateSessionLua.Run(
		ctx,
		v.rdb,
		[]string{sessionFamilyKey(famID), userSessionsKey(userID)},
		userID.String(),
		presentedJTI,
		newJTI,
		sec,
		famID,
		"",
		epoch,
	).Text()
	if err != nil {
		return "", err
	}
	return rotateOutcome(res), nil
}

func (v *valkeyStore) RevokeSession(ctx context.Context, userID uuid.UUID, famID string) error {
	return revokeSessionLua.Run(ctx, v.rdb, []string{sessionFamilyKey(famID), userSessionsKey(userID)}, famID).Err()
}

func (v *valkeyStore) RevokeAllUserSessions(ctx context.Context, userID uuid.UUID) error {
	return revokeAllUserSessionsLua.Run(ctx, v.rdb, []string{userSessionsKey(userID)}).Err()
}

func (v *valkeyStore) RevokeUserSessionsBeforeEpoch(ctx context.Context, userID uuid.UUID, minValidEpoch int64) error {
	return revokeUserSessionsBeforeEpochLua.Run(ctx, v.rdb, []string{userSessionsKey(userID)}, minValidEpoch).Err()
}
