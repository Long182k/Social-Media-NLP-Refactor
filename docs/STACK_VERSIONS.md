# Stack versions

Use these versions for local development and CI. The [OpenSpec change](../../openspec/changes/social-media-nlp-refactor/design.md) defines the milestone boundaries.

| Tool | Version |
|---|---|
| Go | `1.27.4` |
| sqlc | `v1.31.1` |
| goose | `v3.30.1` |
| Node.js | `v26.13.1` |
| pnpm | `12.21.0` |

```bash
go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1
go install github.com/pressly/goose/v3/cmd/goose@v3.30.1
```

| Milestone | Service | Registry reference |
|---|---|---|
| 1–2 | PostgreSQL | `postgres:19-alpine@sha256:8b0ff4cdf221f0c53d7fcda681d3b461be725c492cd881c5ec9c0ad25023f188` |
| 1–2 | Valkey | `valkey/valkey:9.2-alpine@sha256:c8e2d17498838af5d91e12eb7c11220b3434a7b7131a333e3af52ad5e0b80882` |
| 1–2 | MinIO server and bucket setup | `cgr.dev/chainguard/minio:latest@sha256:e9cf6b99c226e9539ed0f14b6f00e37b07da153c7ba1f4f8709c807574b471ec` |
| 1–2 | Mailpit | `axllent/mailpit:v1.34@sha256:23a2ef85bc50853ab3093645d1d64679afd8887e89da5e48a88940c172591a4e` |
| 4 | VictoriaMetrics | `victoriametrics/victoria-metrics:v1.156.0@sha256:da8a90ac0912f16589be2b96a6441a2fca4bca415c83783d972d92e3b5b66970` |
| 4 | Grafana | `grafana/grafana:13.3.1-slim@sha256:49221b454a407632e47e6d4bbc2bb1585c462bf68c6fb9ebed4aa072b027595a` |

The development Compose stack uses a dedicated PostgreSQL 19 volume. The benchmark recreates PostgreSQL and Valkey for each leg. Go modules and frontend dependencies are locked in `backend/go.sum` and `frontend/pnpm-lock.yaml`. The legacy benchmark uses commit `4ad1032f02fce8cb1e2c8b8c1fe293c6cdf4fdc6` from the sibling checkout.
