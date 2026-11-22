# Social-Media-NLP-Refactor

Refactored social media and sentiment analysis platform with a Go 1.27.4 backend, PostgreSQL 19, Valkey 9.2, MinIO, Mailpit, and a React 19, Vite 8, TypeScript, and Tailwind CSS v4 frontend.

## Local Stack

Install the pinned tools and use the service versions in the [stack manifest](docs/STACK_VERSIONS.md).
The PostgreSQL 19 stack initializes a fresh volume and keeps the previous PostgreSQL 16 volume available.

1. Copy `.env.example` to `.env` and populate `JWT_SECRET` and `REFRESH_JWT_SECRET` with strong random values (the Go API refuses to start if either secret is missing or set to a legacy or swapped placeholder). `cmd/api` and `cmd/seed` automatically load `.env` from the working directory or repo root (`../.env`) when present without overriding already-exported environment variables.
2. Start local infrastructure:
   ```bash
   kiri compose -f deploy/compose.yaml up -d
   kiri compose -f deploy/compose.yaml ps
   ```
3. Run database migrations (`goose`) and generate `sqlc` queries:
   ```bash
   goose -dir backend/db/migrations postgres "postgres://postgres:postgres@127.0.0.1:5432/social_media?sslmode=disable" up
   cd backend && sqlc generate && go build ./...
   ```
4. Seed deterministic Milestone 1 fixtures (105 users, 110 VI/EN posts; idempotent):
   ```bash
   cd backend && go run ./cmd/seed
   ```
5. Start the Go API server (from repo root or `backend/`):
   ```bash
   cd backend && go run ./cmd/api
   ```
6. Start the frontend at `http://127.0.0.1:5173`. Vite proxies `/api`, `/graphql`, `/healthz`, `/readyz`, and `/ws` to the Go server on port 8080.
   ```bash
   cd frontend && pnpm install --frozen-lockfile && pnpm dev
   ```

## Local Endpoints & Ports

- Frontend Dev Server: `http://127.0.0.1:5173`
- Go API: `http://127.0.0.1:8080` (`/healthz`, `/readyz`, `/metrics`, `/api/*`, `/graphql`, `/ws`)
- PostgreSQL 19: `127.0.0.1:5432`
- Valkey 9.2: `127.0.0.1:6379`
- MinIO S3 / Console: `http://127.0.0.1:9000` / `http://127.0.0.1:9001`
- Mailpit SMTP / Web UI: `127.0.0.1:1025` / `http://127.0.0.1:8025`

## Sentiment evaluation

Run `cd backend && go run ./cmd/nlp-eval` for the local Vietnamese and English benchmark. It reports macro-F1 and F1 by label for the bundled 60 examples.

Cloud refinement is disabled by default. Before evaluating the Workers Free models, export `VALKEY_URL`, `NLP_CLOUD_TOKEN`, and `NLP_CLOUD_EVIDENCE`. The evidence file uses the JSON fields in [Evidence](backend/internal/nlp/worker/policy.go). It records an account check from the last 24 hours, including no payment method or AI credits, both Free models, and the shared daily quota. All inference callers on the account must share a Valkey ledger with no eviction and durable writes. The local Compose instance is configured for local scoring.

Run `go run ./cmd/nlp-eval --initialize-budget` once per UTC day using a dashboard usage reading from the last five minutes. If that day's ledger is missing, inference stays local. Run `go run ./cmd/nlp-eval --cloud` to measure both models against the shared 9,000-neuron cap. The JSON report includes input and output token counts, measured neurons, reservations, and macro-F1 for each model. To enable background refinement, both models must beat the bundled baseline. Supply the successful report through `NLP_CLOUD_EVALUATION` and set `ENABLE_CLOUD_NLP=true` when starting the API. Expired account evidence or an unavailable budget keeps the locally computed result.

## Verification

Backend tests use the local PostgreSQL, Valkey, Mailpit, and MinIO stack. Apply all migrations before running them.

```bash
cd backend
sqlc generate && sqlc diff && go vet ./... && go build ./...
go test -count=1 -race ./...
```

Frontend type-check, lint, production build, and headless Playwright E2E suite (ensure Kiri Compose is up and `goose ... up` has migrated `social_media`; E2E self-provisions its test users and pagination fixtures without requiring prior `cmd/seed`):

```bash
cd frontend
pnpm tsc --noEmit && pnpm lint && pnpm build
pnpm test:sequence
pnpm test:e2e
```

Old-vs-New Milestone 1 benchmark (runs in an isolated Compose project `smn-bench-*` without modifying the dev/E2E stack or `/home/drakez/Projects/Social-Media-NLP`):

```bash
./scripts/bench/bench-m1.sh --json-out docs/bench-m1-results.json
```

## Container and monitoring

Run the database migrations before starting the full profile. The Go image serves the SPA and API at `http://127.0.0.1:8080`. VictoriaMetrics runs at `http://127.0.0.1:8428` and Grafana at `http://127.0.0.1:3001`.

```bash
kiri build -f backend/Dockerfile -t social-media-nlp-api:m4-local .
kiri compose --profile full -f deploy/compose.yaml up -d --no-build
```

On Linux hosts where the local firewall blocks traffic between bridge containers, add `-f deploy/compose.kiri-linux.yaml` to the Compose command. The override uses loopback endpoints for the API and monitoring. The default Compose setup signs media URLs for the browser endpoint and performs object operations over the internal network.

The [Kustomize base](deploy/k8s/base) and two overlays provide the local single-replica lab and a three-replica HA reference. The API deployment reads the `social-media-api` Secret in its namespace for PostgreSQL, Valkey, and session credentials. The local Kiri context can use the built image after importing its Docker archive into containerd.

```bash
kiri k8s start
kiri save social-media-nlp-api:m4-local | kiri exec -i kiri-k8s ctr --address /run/k3s/containerd/containerd.sock --namespace k8s.io images import -
kubectl kustomize deploy/k8s/overlays/local-kiri
kubectl kustomize deploy/k8s/overlays/ha-prod
```

See the [release evidence and smoke workflow](docs/RELEASE_VERIFICATION.md) for Render Free. Run `./scripts/bench/bench-m1.sh --full-parity` to reproduce the [Milestone 4 benchmark](docs/bench-m4-results.json).
