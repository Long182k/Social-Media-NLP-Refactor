# Render Free release verification

The local stack uses PostgreSQL, Valkey, MinIO, Mailpit, VictoriaMetrics, and Grafana. The Render blueprint describes the hosted demo with a single origin, secure refresh cookies, and automatic deploys off. Hosted provisioning and smoke verification require fresh provider evidence before use.

## Evidence for a release

The `render-free` GitHub environment holds `NOCARD_RELEASE_EVIDENCE` as an operator-verified JSON record. Evidence covers the commit being released and observed account settings from the last 24 hours. The required keys and exact values are enforced by [release-free.py](../scripts/release-free.py).

| Field | Required evidence |
|---|---|
| `releaseSha` and `qa.sha` | The same full Git commit SHA as the workflow ref and CI run. |
| `qa.verifiedAt`, `qa.playwright`, `qa.playwrightMcp` | Timestamp and passing automated and interactive local full-stack runs on that SHA. Use `passed` for each completed check. |
| `providers` | Render, Neon, Upstash, Resend, Supabase, Cloudflare Workers, and GitHub Actions. Grafana Cloud is optional. |
| Each provider record | `plan`, `verifiedAt`, `source`, `noPaymentMethod=true`, `hardQuotaStop=true`, and `overageBilling=false`. The source identifies the account dashboard evidence. |
| Render record | The verified `serviceId` and `autoDeployOff=true`. The workflow confirms the Free plan, repository, and disabled automation with the Render API. |
| Upstash record | `noEviction=true` and `durableWrites=true`. The workflow checks GETDEL, Lua and Pub/Sub over TLS before triggering Render. |
| GitHub Actions record | `spendLimitUSD=0` and `freeMinutesHardStop=true`. Workflows run on standard GitHub-hosted runners only for public repositories. |

The deployment workflow also requires `RENDER_API_KEY`, `UPSTASH_RELEASE_URL`, and credentials for the Resend account owner's demo user. Store them as environment secrets. Apply the project's Goose migrations to the verified hosted PostgreSQL database and verify the same-origin URLs, S3 bucket policy, S3 CORS, and the demo owner account before recording release readiness.

The workflow checks a successful `quality-security` job on the exact SHA, sends Render that SHA via `commitId`, and waits for the API to report it live. Hosted Chromium allows 105 seconds for the first readiness probe and checks secure cookie rehydration, feed and media writes, WebSocket ticket and catch-up, immediate local sentiment, and the Resend owner and third-party responses. The owner reset sends one email to the verified owner.

The current checkout has no remote, approved release commit, or fresh provider account evidence. Local verification used MinIO and Mailpit, an HTTP stub for Resend, and real local PostgreSQL and Valkey. Hosted provisioning, smoke, and model evaluation are pending.
