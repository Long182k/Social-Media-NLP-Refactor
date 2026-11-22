#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/../.." && pwd -P)"
LEGACY_ROOT="${LEGACY_ROOT:-/home/drakez/Projects/Social-Media-NLP}"
readonly LEGACY_PINNED_COMMIT="4ad1032f02fce8cb1e2c8b8c1fe293c6cdf4fdc6"

BENCH_COMPOSE_FILE="$REPO_ROOT/deploy/bench-compose.yaml"
readonly BENCH_PROJECT="smn-bench-$$"
BENCH_PG_PORT="${BENCH_PG_PORT:-15432}"
BENCH_VALKEY_PORT="${BENCH_VALKEY_PORT:-16379}"

LEGACY_DB="social_media_legacy"
V2_BENCH_DB="social_media_v2_bench"
LEGACY_PORT="${LEGACY_PORT:-13000}"
NEW_PORT="${NEW_PORT:-18080}"
COLD_TRIALS="${COLD_TRIALS:-5}"
WARMUP_REQS="${WARMUP_REQS:-50}"
FEED_REQS="${FEED_REQS:-500}"
OUT_JSON="${OUT_JSON:-}"
FULL_PARITY=0
PARITY_COUNTS=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --full-parity)
      FULL_PARITY=1
      shift
      ;;
    --json-out)
      [[ $# -ge 2 ]] || { echo "Error: --json-out requires a path." >&2; exit 2; }
      OUT_JSON="$2"
      shift 2
      ;;
    *)
      echo "Unknown argument: $1" >&2
      exit 2
      ;;
  esac
done

if [[ "$FULL_PARITY" == 1 ]]; then
  OUT_JSON="${OUT_JSON:-$REPO_ROOT/docs/bench-m4-results.json}"
else
  OUT_JSON="${OUT_JSON:-$REPO_ROOT/docs/bench-m1-results.json}"
fi

seed_full_parity() {
  bcompose exec -T postgres psql -U postgres -d "$1" -v "legacy=$2" -qtA \
    < "$SCRIPT_DIR/fixtures-m4.sql"
}

for cmd in git kiri docker go node pnpm python3 goose; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "ERROR: required command '$cmd' not found in PATH." >&2
    exit 1
  fi
done

if [[ ! -f "$BENCH_COMPOSE_FILE" ]]; then
  echo "ERROR: isolated benchmark compose file missing at '$BENCH_COMPOSE_FILE'." >&2
  exit 1
fi

if [[ ! -d "$LEGACY_ROOT/BE-Social-Media" || ! -d "$LEGACY_ROOT/FE-Social-Media" ]]; then
  echo "ERROR: Legacy repo not found at '$LEGACY_ROOT'." >&2
  exit 1
fi

if [[ -n "$(git -C "$LEGACY_ROOT" status --porcelain)" ]]; then
  echo "ERROR: Legacy repo '$LEGACY_ROOT' has uncommitted changes before benchmark start." >&2
  exit 1
fi

ACTUAL_LEGACY_COMMIT="$(git -C "$LEGACY_ROOT" rev-parse HEAD)"
if [[ "$ACTUAL_LEGACY_COMMIT" != "$LEGACY_PINNED_COMMIT" ]]; then
  echo "ERROR: Legacy repo HEAD '$ACTUAL_LEGACY_COMMIT' does not match pinned baseline '$LEGACY_PINNED_COMMIT'." >&2
  exit 1
fi

TMP_LEGACY="$(mktemp -d /tmp/legacy-bench.XXXXXX)"
BIN_DIR="$TMP_LEGACY/.bench-bin"
mkdir -p "$BIN_DIR"
LEGACY_PID=""
NEW_PID=""

bcompose() {
  BENCH_PG_PORT="$BENCH_PG_PORT" BENCH_VALKEY_PORT="$BENCH_VALKEY_PORT" \
    kiri compose -p "$BENCH_PROJECT" -f "$BENCH_COMPOSE_FILE" "$@"
}

cleanup() {
  set +e
  if [[ -n "$LEGACY_PID" ]] && kill -0 "$LEGACY_PID" 2>/dev/null; then
    kill -TERM "$LEGACY_PID" 2>/dev/null
    wait "$LEGACY_PID" 2>/dev/null
  fi
  if [[ -n "$NEW_PID" ]] && kill -0 "$NEW_PID" 2>/dev/null; then
    kill -TERM "$NEW_PID" 2>/dev/null
    wait "$NEW_PID" 2>/dev/null
  fi

  if [[ "$BENCH_PROJECT" == smn-bench-* ]]; then
    bcompose down -v --remove-orphans >/dev/null 2>&1 || true
  fi
  rm -rf "$TMP_LEGACY"

  if [[ -n "$(git -C "$LEGACY_ROOT" status --porcelain)" ]]; then
    echo "ERROR: Legacy repo '$LEGACY_ROOT' was modified!" >&2
    exit 1
  fi
}
trap cleanup EXIT

log() {
  printf '[bench-m1] %s\n' "$*" >&2
}

sample_proc_mem_mib() {
  local pid="$1"
  python3 - "$pid" <<'PY'
import json, sys
pid = sys.argv[1]
rss_kib = 0
hwm_kib = 0
with open(f"/proc/{pid}/status", "r", encoding="utf-8") as f:
    for line in f:
        if line.startswith("VmRSS:"):
            rss_kib = int(line.split()[1])
        elif line.startswith("VmHWM:"):
            hwm_kib = int(line.split()[1])
if rss_kib <= 0 or hwm_kib <= 0:
    raise SystemExit(f"invalid /proc/{pid}/status memory sample: rss={rss_kib} hwm={hwm_kib}")
print(json.dumps({
    "rss_kib": rss_kib,
    "rss_mib": round(rss_kib / 1024.0, 2),
    "hwm_kib": hwm_kib,
    "hwm_mib": round(hwm_kib / 1024.0, 2),
}))
PY
}

sample_compose_mem_mib() {
  local pg_id vk_id raw
  pg_id="$(bcompose ps -q postgres | tr -d '\r')"
  vk_id="$(bcompose ps -q valkey | tr -d '\r')"

  if [[ -z "$pg_id" || -z "$vk_id" ]]; then
    echo "ERROR: failed to resolve exact benchmark container IDs (postgres='$pg_id', valkey='$vk_id')." >&2
    exit 1
  fi
  if [[ "$(printf '%s\n' "$pg_id" | wc -l)" -ne 1 || "$(printf '%s\n' "$vk_id" | wc -l)" -ne 1 ]]; then
    echo "ERROR: expected single container ID per service (postgres='$pg_id', valkey='$vk_id')." >&2
    exit 1
  fi

  raw="$(docker stats --no-stream --no-trunc --format '{{.ID}}\t{{.MemUsage}}' "$pg_id" "$vk_id")"
  python3 - "$pg_id" "$vk_id" "$raw" <<'PY'
import json, re, sys

pg_id = sys.argv[1].strip()
vk_id = sys.argv[2].strip()
raw = sys.argv[3]

def parse_to_mib(token: str) -> float:
    m = re.match(r"^([0-9.]+)\s*([KMG]?i?B)$", token.strip(), re.I)
    if not m:
        raise ValueError(f"unparseable memory token: {token!r}")
    val = float(m.group(1))
    unit = m.group(2).lower()
    factors = {
        "b": 1.0 / (1024 * 1024),
        "kib": 1.0 / 1024,
        "mib": 1.0,
        "gib": 1024.0,
        "kb": 1000.0 / (1024 * 1024),
        "mb": 1000000.0 / (1024 * 1024),
        "gb": 1000000000.0 / (1024 * 1024),
    }
    if unit not in factors:
        raise ValueError(f"unknown memory unit: {unit!r}")
    mib = round(val * factors[unit], 2)
    if mib <= 0.0:
        raise ValueError(f"non-positive memory sample: {mib}")
    return mib

pg_matches = []
vk_matches = []
for line in raw.splitlines():
    parts = line.split("\t")
    if len(parts) < 2:
        continue
    cid = parts[0].strip()
    used_str = parts[1].split("/")[0].strip()
    if cid == pg_id:
        pg_matches.append(parse_to_mib(used_str))
    elif cid == vk_id:
        vk_matches.append(parse_to_mib(used_str))

if len(pg_matches) != 1 or len(vk_matches) != 1:
    raise SystemExit(
        f"ERROR: expected exactly 1 exact container ID match per service "
        f"(pg={pg_matches!r}, vk={vk_matches!r}, raw={raw!r})"
    )

print(json.dumps({"postgres_mib": pg_matches[0], "valkey_mib": vk_matches[0]}))
PY
}

wait_infra_healthy() {
  local i
  for i in $(seq 1 60); do
    if bcompose exec -T postgres pg_isready -U postgres >/dev/null 2>&1 \
      && bcompose exec -T valkey valkey-cli PING 2>/dev/null | grep -q PONG; then
      return 0
    fi
    sleep 0.5
  done
  echo "ERROR: isolated benchmark postgres/valkey failed to become healthy." >&2
  exit 1
}

reset_isolated_infra() {
  bcompose down -v --remove-orphans >/dev/null 2>&1 || true
  bcompose up -d >/dev/null
  wait_infra_healthy
}

postgres_version() {
  bcompose exec -T postgres psql -U postgres -d postgres -Atc "SHOW server_version"
}

valkey_version() {
  bcompose exec -T valkey valkey-cli INFO server |
    awk -F: '/^valkey_version:/ {sub(/\r$/, "", $2); print $2}'
}

valkey_eviction_policy() {
  bcompose exec -T valkey valkey-cli --raw CONFIG GET maxmemory-policy | tail -n 1
}

log "Building Go v2 API, benchload, and seed binaries into $BIN_DIR..."
(
  cd "$REPO_ROOT/backend"
  go build -o "$BIN_DIR/new-api" ./cmd/api
  go build -o "$BIN_DIR/benchload" ./cmd/benchload
  go build -o "$BIN_DIR/seed-v2" ./cmd/seed
)

log "Building v2 frontend dist..."
(
  cd "$REPO_ROOT/frontend"
  pnpm build >/dev/null
)

log "Extracting pinned legacy commit $ACTUAL_LEGACY_COMMIT via git archive into $TMP_LEGACY..."
git -C "$LEGACY_ROOT" archive --format=tar "$ACTUAL_LEGACY_COMMIT" BE-Social-Media FE-Social-Media | tar -x -C "$TMP_LEGACY"

log "Installing and building Legacy BE in $TMP_LEGACY/BE-Social-Media..."
(
  cd "$TMP_LEGACY/BE-Social-Media"
  pnpm install --frozen-lockfile \
    --allow-build=@apollo/protobufjs \
    --allow-build=@nestjs/core \
    --allow-build=@prisma/client \
    --allow-build=@prisma/engines \
    --allow-build=@swc/core \
    --allow-build=argon2 \
    --allow-build=esbuild \
    --allow-build=prisma >/dev/null
  pnpm build >/dev/null
)

log "Installing and building Legacy FE in $TMP_LEGACY/FE-Social-Media..."
(
  cd "$TMP_LEGACY/FE-Social-Media"
  pnpm install --frozen-lockfile \
    --allow-build=@swc/core \
    --allow-build=core-js \
    --allow-build=esbuild >/dev/null
  VITE_SERVER_URL="http://127.0.0.1:${LEGACY_PORT}/api" pnpm build >/dev/null
)

LEGACY_DB_URL="postgresql://postgres:postgres@127.0.0.1:${BENCH_PG_PORT}/${LEGACY_DB}?sslmode=disable"
V2_BENCH_DB_URL="postgres://postgres:postgres@127.0.0.1:${BENCH_PG_PORT}/${V2_BENCH_DB}?sslmode=disable"
ALICE_UUID="$("$BIN_DIR/benchload" -mode=alice-uuid)"
log "Pinned Legacy Commit: $ACTUAL_LEGACY_COMMIT | Deterministic Alice UUID: $ALICE_UUID"

# ==============================================================================
# PHASE 1: LEGACY (NestJS 10 + Prisma + node-nlp) in fresh isolated containers
# ==============================================================================
log "Starting fresh isolated Compose project '$BENCH_PROJECT' (ports pg=$BENCH_PG_PORT, valkey=$BENCH_VALKEY_PORT) for Legacy..."
reset_isolated_infra
BENCH_POSTGRES_VERSION="$(postgres_version)"
BENCH_VALKEY_VERSION="$(valkey_version)"
if [[ -z "$BENCH_POSTGRES_VERSION" || -z "$BENCH_VALKEY_VERSION" || "$(valkey_eviction_policy)" != noeviction ]]; then
  log "ERROR: Unexpected PostgreSQL or Valkey configuration in the legacy benchmark stack."
  exit 1
fi

log "Creating & seeding isolated Legacy database ($LEGACY_DB)..."
bcompose exec -T postgres psql -U postgres -d postgres -v ON_ERROR_STOP=1 \
  -c "CREATE DATABASE $LEGACY_DB;" >/dev/null
(
  cd "$TMP_LEGACY/BE-Social-Media"
  DATABASE_URL="$LEGACY_DB_URL" npx prisma db push --skip-generate --accept-data-loss >/dev/null
)
LEGACY_DATABASE_URL="$LEGACY_DB_URL" "$REPO_ROOT/scripts/bench/seed-legacy.sh"
LEGACY_COUNTS="$(bcompose exec -T postgres psql -U postgres -d "$LEGACY_DB" -tAc "SELECT (SELECT count(*) FROM users) || ',' || (SELECT count(*) FROM posts);")"
if [[ "$LEGACY_COUNTS" != "105,110" ]]; then
  echo "ERROR: Unexpected legacy seed counts '$LEGACY_COUNTS' (want 105,110)." >&2
  exit 1
fi
if [[ "$FULL_PARITY" == 1 ]]; then
  PARITY_COUNTS="$(seed_full_parity "$LEGACY_DB" 1)"
  [[ "$PARITY_COUNTS" == "12,24,122,12,12,12,12,12,24,12,24,36,12,36" ]]
fi
bcompose exec -T postgres psql -U postgres -d "$LEGACY_DB" -c "ANALYZE;" >/dev/null
bcompose exec -T valkey valkey-cli FLUSHALL >/dev/null
sleep 1

cat >"$TMP_LEGACY/slowbuffer-shim.cjs" <<'CJS'
const buf = require('buffer');
if (typeof buf.SlowBuffer === 'undefined') {
  buf.SlowBuffer = function SlowBuffer(size) {
    return buf.Buffer.allocUnsafe(size);
  };
}
CJS

LEGACY_ENV=(
  "NODE_OPTIONS=--require=${TMP_LEGACY}/slowbuffer-shim.cjs"
  "IS_PRODUCTION=true"
  "PORT=${LEGACY_PORT}"
  "REDIS_HOST=127.0.0.1"
  "REDIS_PORT=${BENCH_VALKEY_PORT}"
  "DATABASE_URL=${LEGACY_DB_URL}"
  "JWT_SECRET=$(python3 -c 'import secrets; print(secrets.token_hex(32))')"
  "REFRESH_JWT_SECRET=$(python3 -c 'import secrets; print(secrets.token_hex(32))')"
  "EMAIL_HOST=127.0.0.1"
  "EMAIL_PORT=1025"
  "EMAIL_USER=bench"
  "EMAIL_PASSWORD=bench"
  "CLOUDINARY_CLOUD_NAME=bench"
  "CLOUDINARY_API_KEY=bench"
  "CLOUDINARY_API_SECRET=bench"
  "FRONTEND_URL=http://127.0.0.1:5173"
)

log "Measuring Legacy cold-start ($COLD_TRIALS trials, validating DB-backed profile + Valkey write)..."
LEGACY_COLD_JSON="$(env "${LEGACY_ENV[@]}" "$BIN_DIR/benchload" \
  -mode=coldstart \
  -trials="$COLD_TRIALS" \
  -url="http://127.0.0.1:${LEGACY_PORT}/api/auth/profile/${ALICE_UUID}" \
  -probe-mode=legacy-deps \
  -expect-id="$ALICE_UUID" \
  -valkey-url="redis://127.0.0.1:${BENCH_VALKEY_PORT}/0" \
  -workdir="$TMP_LEGACY/BE-Social-Media" \
  -- node dist/main)"

bcompose exec -T valkey valkey-cli FLUSHALL >/dev/null

log "Starting Legacy API for idle RAM, feed latency, Valkey proof, and post-load RAM..."
(
  cd "$TMP_LEGACY/BE-Social-Media"
  exec env "${LEGACY_ENV[@]}" node dist/main >"$TMP_LEGACY/legacy-api.log" 2>&1
) &
LEGACY_PID=$!

"$BIN_DIR/benchload" \
  -mode=wait-ready \
  -url="http://127.0.0.1:${LEGACY_PORT}/api/auth/profile/${ALICE_UUID}" \
  -probe-mode=legacy-deps \
  -expect-id="$ALICE_UUID" \
  -valkey-url="redis://127.0.0.1:${BENCH_VALKEY_PORT}/0" \
  -timeout=30s
bcompose exec -T valkey valkey-cli FLUSHALL >/dev/null
sleep 2

LEGACY_API_MEM_IDLE="$(sample_proc_mem_mib "$LEGACY_PID")"
LEGACY_INFRA_MEM_IDLE="$(sample_compose_mem_mib)"

log "Running Legacy feed benchmark (c=10, warmup=$WARMUP_REQS, reqs=$FEED_REQS)..."
LEGACY_FEED_C10="$("$BIN_DIR/benchload" -mode=feed -base-url="http://127.0.0.1:${LEGACY_PORT}" -warmup="$WARMUP_REQS" -requests="$FEED_REQS" -concurrency=10 -limit=10 -expected-count=10)"

log "Running Legacy feed benchmark (c=1, warmup=$WARMUP_REQS, reqs=$FEED_REQS)..."
LEGACY_FEED_C1="$("$BIN_DIR/benchload" -mode=feed -base-url="http://127.0.0.1:${LEGACY_PORT}" -warmup="$WARMUP_REQS" -requests="$FEED_REQS" -concurrency=1 -limit=10 -expected-count=10)"

LEGACY_VALKEY_KEYS="$(bcompose exec -T valkey valkey-cli --raw KEYS 'refresh:*' | tr -d '\r' | sed '/^$/d')"
if [[ -z "$LEGACY_VALKEY_KEYS" ]]; then
  echo "ERROR: Legacy login did not write expected refresh:* key to isolated benchmark Valkey." >&2
  exit 1
fi

LEGACY_API_MEM_POST="$(sample_proc_mem_mib "$LEGACY_PID")"
LEGACY_INFRA_MEM_POST="$(sample_compose_mem_mib)"

kill -TERM "$LEGACY_PID" 2>/dev/null || true
wait "$LEGACY_PID" 2>/dev/null || true
LEGACY_PID=""

log "Measuring Legacy node-nlp SentimentAnalyzer latency (30 warmup + 500 calls)..."
LEGACY_NLP_JSON="$(cd "$TMP_LEGACY/BE-Social-Media" && node - <<'NODE'
const { SentimentAnalyzer } = require('node-nlp');
async function main() {
  const tInit0 = process.hrtime.bigint();
  const langs = ['en', 'es', 'fr', 'it', 'nl', 'id', 'pt', 'de', 'ja', 'zh'];
  const analyzers = new Map(langs.map((l) => [l, new SentimentAnalyzer({ language: l })]));
  const initMs = Number(process.hrtime.bigint() - tInit0) / 1e6;

  function detectLanguage(text) {
    if (/[\u3040-\u30ff]/.test(text)) return 'ja';
    if (/[\u4e00-\u9FFF]/.test(text)) return 'zh';
    if (/[áéíóúüñ¿¡]/.test(text)) return 'es';
    if (/[àâçéèêëîïôûùüÿñ]/.test(text)) return 'fr';
    if (/[àèéìíîòóùú]/.test(text)) return 'it';
    if (/[äöüß]/.test(text)) return 'de';
    return 'en';
  }

  async function evaluateContent(content) {
    const lang = detectLanguage(content);
    const analyzer = analyzers.get(lang) || analyzers.get('en');
    const result = await analyzer.getSentiment(content);
    const thresholds = {
      positive: lang === 'ja' || lang === 'zh' ? 0.2 : 0.3,
      negative: lang === 'ja' || lang === 'zh' ? -0.2 : -0.3,
    };
    if (result.score >= thresholds.positive) return 'GOOD';
    if (result.score <= thresholds.negative) return 'BAD';
    return 'MODERATE';
  }

  const viSample = 'Hôm nay ứng dụng chạy cực kỳ mượt mà và giao diện xịn xò quá trời, nhưng hôm qua hơi lag nhẹ chút thôi ❤️';
  const enSample = 'The new Go backend is super fast, really clean, and not buggy at all! Love this awesome upgrade 🔥';

  for (let i = 0; i < 30; i++) {
    await evaluateContent(viSample);
    await evaluateContent(enSample);
  }

  async function bench(sample, n) {
    const times = [];
    let label = 'MODERATE';
    for (let i = 0; i < n; i++) {
      const t0 = process.hrtime.bigint();
      label = await evaluateContent(sample);
      times.push(Number(process.hrtime.bigint() - t0) / 1e6);
    }
    times.sort((a, b) => a - b);
    const mean = times.reduce((a, b) => a + b, 0) / times.length;
    const p50 = times[Math.floor(0.50 * (times.length - 1))];
    const p95 = times[Math.floor(0.95 * (times.length - 1))];
    return {
      warmup: 30,
      iters: n,
      mean_ms: Number(mean.toFixed(4)),
      p50_ms: Number(p50.toFixed(4)),
      p95_ms: Number(p95.toFixed(4)),
      min_ms: Number(times[0].toFixed(4)),
      max_ms: Number(times[times.length - 1].toFixed(4)),
      detected_lang: detectLanguage(sample),
      label,
    };
  }

  const vi = await bench(viSample, 500);
  const en = await bench(enSample, 500);
  console.log(JSON.stringify({ init_ms: Number(initMs.toFixed(3)), vi, en }));
}
main().catch((err) => { console.error(err); process.exit(1); });
NODE
)"

# ==============================================================================
# PHASE 2: NEW (Go 1.27 + pgxpool + sqlc) in fresh isolated containers
# ==============================================================================
log "Recreating fresh isolated Compose containers for New Go v2 benchmark..."
reset_isolated_infra
if [[ "$(postgres_version)" != "$BENCH_POSTGRES_VERSION" ||
      "$(valkey_version)" != "$BENCH_VALKEY_VERSION" ||
      "$(valkey_eviction_policy)" != noeviction ]]; then
  log "ERROR: The two benchmark stacks must use the same PostgreSQL and Valkey versions and noeviction policy."
  exit 1
fi

log "Creating, migrating, and seeding isolated v2 database ($V2_BENCH_DB)..."
bcompose exec -T postgres psql -U postgres -d postgres -v ON_ERROR_STOP=1 \
  -c "CREATE DATABASE $V2_BENCH_DB;" >/dev/null
goose -dir "$REPO_ROOT/backend/db/migrations" postgres "$V2_BENCH_DB_URL" up >/dev/null
DATABASE_URL="$V2_BENCH_DB_URL" "$BIN_DIR/seed-v2"
V2_COUNTS="$(bcompose exec -T postgres psql -U postgres -d "$V2_BENCH_DB" -tAc "SELECT (SELECT count(*) FROM users) || ',' || (SELECT count(*) FROM posts);")"
if [[ "$V2_COUNTS" != "105,110" ]]; then
  echo "ERROR: Unexpected v2 bench seed counts '$V2_COUNTS' (want 105,110)." >&2
  exit 1
fi
if [[ "$FULL_PARITY" == 1 ]]; then
  V2_PARITY_COUNTS="$(seed_full_parity "$V2_BENCH_DB" 0)"
  [[ "$V2_PARITY_COUNTS" == "$PARITY_COUNTS" ]]
fi
bcompose exec -T postgres psql -U postgres -d "$V2_BENCH_DB" -c "ANALYZE;" >/dev/null
bcompose exec -T valkey valkey-cli FLUSHALL >/dev/null
sleep 1

NEW_ENV=(
  "APP_ENV=benchmark"
  "PORT=${NEW_PORT}"
  "DATABASE_URL=${V2_BENCH_DB_URL}"
  "VALKEY_URL=redis://127.0.0.1:${BENCH_VALKEY_PORT}/0"
  "JWT_SECRET=bench-v2-jwt-secret-0001-super-long"
  "REFRESH_JWT_SECRET=bench-v2-refresh-secret-0001-super-long"
  "SMTP_HOST=127.0.0.1"
  "SMTP_PORT=1025"
  "LOG_LEVEL=warn"
)

log "Measuring New Go API cold-start ($COLD_TRIALS trials, validating /readyz + Postgres/Valkey login persistence)..."
NEW_COLD_JSON="$(env "${NEW_ENV[@]}" "$BIN_DIR/benchload" \
  -mode=coldstart \
  -trials="$COLD_TRIALS" \
  -url="http://127.0.0.1:${NEW_PORT}/readyz" \
  -probe-mode=v2-deps \
  -expect-id="$ALICE_UUID" \
  -valkey-url="redis://127.0.0.1:${BENCH_VALKEY_PORT}/0" \
  -workdir="$REPO_ROOT/backend" \
  -- "$BIN_DIR/new-api")"

bcompose exec -T valkey valkey-cli FLUSHALL >/dev/null

log "Starting New Go API for idle RAM, feed latency, and post-load RAM..."
(
  cd "$REPO_ROOT/backend"
  exec env "${NEW_ENV[@]}" "$BIN_DIR/new-api" >"$TMP_LEGACY/new-api.log" 2>&1
) &
NEW_PID=$!

"$BIN_DIR/benchload" \
  -mode=wait-ready \
  -url="http://127.0.0.1:${NEW_PORT}/readyz" \
  -probe-mode=v2-deps \
  -expect-id="$ALICE_UUID" \
  -valkey-url="redis://127.0.0.1:${BENCH_VALKEY_PORT}/0" \
  -timeout=30s
bcompose exec -T valkey valkey-cli FLUSHALL >/dev/null
sleep 2

NEW_API_MEM_IDLE="$(sample_proc_mem_mib "$NEW_PID")"
NEW_INFRA_MEM_IDLE="$(sample_compose_mem_mib)"

log "Running New Go feed benchmark (c=10, warmup=$WARMUP_REQS, reqs=$FEED_REQS)..."
NEW_FEED_C10="$("$BIN_DIR/benchload" -mode=feed -base-url="http://127.0.0.1:${NEW_PORT}" -warmup="$WARMUP_REQS" -requests="$FEED_REQS" -concurrency=10 -limit=10 -expected-count=10)"

log "Running New Go feed benchmark (c=1, warmup=$WARMUP_REQS, reqs=$FEED_REQS)..."
NEW_FEED_C1="$("$BIN_DIR/benchload" -mode=feed -base-url="http://127.0.0.1:${NEW_PORT}" -warmup="$WARMUP_REQS" -requests="$FEED_REQS" -concurrency=1 -limit=10 -expected-count=10)"

NEW_API_MEM_POST="$(sample_proc_mem_mib "$NEW_PID")"
NEW_INFRA_MEM_POST="$(sample_compose_mem_mib)"

kill -TERM "$NEW_PID" 2>/dev/null || true
wait "$NEW_PID" 2>/dev/null || true
NEW_PID=""

log "Measuring New Go lexicon NLP like-for-like latency (30 warmup + 500 calls) & Go microbenchmark..."
NEW_NLP_CALL_JSON="$("$BIN_DIR/benchload" -mode=nlp -nlp-warmup=30 -nlp-iters=500)"
NEW_NLP_RAW="$(cd "$REPO_ROOT/backend" && go test -run=^$ -bench=BenchmarkScore -benchmem -count=3 ./internal/nlp/lexicon)"

log "Measuring Frontend raw and gzipped bundle sizes..."
BUNDLE_JSON_PATH="$TMP_LEGACY/bundle.json"
python3 "$REPO_ROOT/scripts/bench/bundle-size.py" \
  --legacy-dist "$TMP_LEGACY/FE-Social-Media/dist" \
  --new-dist "$REPO_ROOT/frontend/dist" \
  --json-out "$BUNDLE_JSON_PATH"

log "Assembling final JSON & Markdown benchmark summary..."
HOST_INFO_JSON="$(python3 - <<'PY'
import json, platform, subprocess
cpu_model = "unknown"
cpu_threads = 0
mem_kib = 0
with open("/proc/cpuinfo", "r", encoding="utf-8") as f:
    for line in f:
        if line.startswith("model name") and cpu_model == "unknown":
            cpu_model = line.split(":", 1)[1].strip()
        elif line.startswith("processor"):
            cpu_threads += 1
with open("/proc/meminfo", "r", encoding="utf-8") as f:
    for line in f:
        if line.startswith("MemTotal:"):
            mem_kib = int(line.split()[1])
            break
print(json.dumps({
    "kernel": platform.release(),
    "arch": platform.machine(),
    "cpu_model": cpu_model,
    "cpu_threads": cpu_threads,
    "ram_gib": round(mem_kib / (1024 * 1024), 2),
    "go_version": subprocess.check_output(["go", "version"], text=True).strip(),
    "node_version": subprocess.check_output(["node", "-v"], text=True).strip(),
    "pnpm_version": subprocess.check_output(["pnpm", "-v"], text=True).strip(),
}))
PY
)"

FINAL_REPORT="$(python3 - "$HOST_INFO_JSON" "$ACTUAL_LEGACY_COMMIT" "$BENCH_PROJECT" \
  "$LEGACY_COLD_JSON" "$LEGACY_API_MEM_IDLE" "$LEGACY_INFRA_MEM_IDLE" "$LEGACY_API_MEM_POST" "$LEGACY_INFRA_MEM_POST" "$LEGACY_FEED_C10" "$LEGACY_FEED_C1" "$LEGACY_NLP_JSON" \
  "$NEW_COLD_JSON" "$NEW_API_MEM_IDLE" "$NEW_INFRA_MEM_IDLE" "$NEW_API_MEM_POST" "$NEW_INFRA_MEM_POST" "$NEW_FEED_C10" "$NEW_FEED_C1" "$NEW_NLP_CALL_JSON" "$NEW_NLP_RAW" "$BUNDLE_JSON_PATH" \
  "$BENCH_POSTGRES_VERSION" "$BENCH_VALKEY_VERSION" "$PARITY_COUNTS" <<'PY'
import json, re, statistics, sys
from datetime import datetime, timezone

host = json.loads(sys.argv[1])
legacy_commit = sys.argv[2]
bench_project = sys.argv[3]

def build_stack(cold, api_idle, infra_idle, api_post, infra_post, feed_c10, feed_c1):
    for key in ("postgres_mib", "valkey_mib"):
        if key not in infra_idle or infra_idle[key] <= 0:
            raise SystemExit(f"missing or invalid idle {key}: {infra_idle}")
        if key not in infra_post or infra_post[key] <= 0:
            raise SystemExit(f"missing or invalid post-load {key}: {infra_post}")
    idle_whole = round(api_idle["rss_mib"] + infra_idle["postgres_mib"] + infra_idle["valkey_mib"], 2)
    post_whole = round(api_post["rss_mib"] + infra_post["postgres_mib"] + infra_post["valkey_mib"], 2)
    return {
        "cold_start": cold,
        "ram": {
            "api_idle_rss_mib": api_idle["rss_mib"],
            "api_post_load_rss_mib": api_post["rss_mib"],
            "api_peak_hwm_mib": api_post["hwm_mib"],
            "infra_idle_mib": infra_idle,
            "infra_post_load_mib": infra_post,
            "whole_stack_idle_mib": idle_whole,
            "whole_stack_post_load_mib": post_whole,
        },
        "feed_c10": feed_c10,
        "feed_c1": feed_c1,
    }

legacy = build_stack(
    json.loads(sys.argv[4]), json.loads(sys.argv[5]), json.loads(sys.argv[6]),
    json.loads(sys.argv[7]), json.loads(sys.argv[8]), json.loads(sys.argv[9]), json.loads(sys.argv[10])
)
legacy["nlp"] = json.loads(sys.argv[11])

new = build_stack(
    json.loads(sys.argv[12]), json.loads(sys.argv[13]), json.loads(sys.argv[14]),
    json.loads(sys.argv[15]), json.loads(sys.argv[16]), json.loads(sys.argv[17]), json.loads(sys.argv[18])
)
new["nlp"] = json.loads(sys.argv[19])

nlp_raw = sys.argv[20]
vi_ns, en_ns = [], []
vi_bytes, en_bytes = [], []
for line in nlp_raw.splitlines():
    m = re.match(r"BenchmarkScore(VI|EN)-\d+\s+\d+\s+([0-9.]+)\s+ns/op\s+([0-9.]+)\s+B/op", line.strip())
    if m:
        kind, ns, b = m.group(1), float(m.group(2)), float(m.group(3))
        if kind == "VI":
            vi_ns.append(ns)
            vi_bytes.append(b)
        else:
            en_ns.append(ns)
            en_bytes.append(b)

new["nlp"]["go_microbench"] = {
    "vi_median_ms": round(statistics.median(vi_ns) / 1e6, 6) if vi_ns else None,
    "vi_median_ns": round(statistics.median(vi_ns), 1) if vi_ns else None,
    "vi_bytes_per_op": statistics.median(vi_bytes) if vi_bytes else None,
    "en_median_ms": round(statistics.median(en_ns) / 1e6, 6) if en_ns else None,
    "en_median_ns": round(statistics.median(en_ns), 1) if en_ns else None,
    "en_bytes_per_op": statistics.median(en_bytes) if en_bytes else None,
    "raw_output": nlp_raw.strip(),
}

with open(sys.argv[21], "r", encoding="utf-8") as f:
    bundles = json.load(f)

fixture = {"users": 105, "posts": 110}
if sys.argv[24]:
    names = ("groups", "group_members", "posts", "comments", "likes", "bookmarks", "follows", "events",
             "event_attendees", "chat_rooms", "chat_participants", "chat_messages", "notifications", "attachments")
    fixture.update(zip(names, map(int, sys.argv[24].split(",")), strict=True))
    fixture["home_posts"] = 110

summary = {
    "generated_at_utc": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
    "legacy_commit": legacy_commit,
    "isolated_compose_project": bench_project,
    "infra": {
        "postgres_version": sys.argv[22],
        "valkey_version": sys.argv[23],
        "valkey_maxmemory_policy": "noeviction",
    },
    "host": host,
    "fixture": fixture,
    "legacy": legacy,
    "new": new,
    "bundles": bundles,
}
print(json.dumps(summary, indent=2))
PY
)"

if [[ -n "$OUT_JSON" ]]; then
  mkdir -p "$(dirname -- "$OUT_JSON")"
  printf '%s\n' "$FINAL_REPORT" > "$OUT_JSON"
  log "Saved JSON results to $OUT_JSON"
else
  printf '%s\n' "$FINAL_REPORT"
fi

log "Benchmark complete. Cleanup running on EXIT."
