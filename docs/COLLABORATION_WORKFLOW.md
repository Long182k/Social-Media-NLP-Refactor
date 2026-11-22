# Codex and Antigravity Collaboration Workflow

## Teamwork (2 participants)

| Role | Model | Responsibilities |
|---|---|---|
| Executor | Codex `gpt-6.2-sol`, `model_reasoning_effort="high"` | Maintains OpenSpec and the refactor, runs verification, resolves review findings, and updates shared memory. |
| Reviewer | Antigravity CLI `gemini-4-argon-high` | Independently reviews the artifacts and test evidence for each gate and returns a verdict. |

Codex runs the work in `/home/drakez/Projects`. The refactor checkout is `Social-Media-NLP-Refactor/`, the OpenSpec change is `openspec/changes/social-media-nlp-refactor/`, and the legacy comparison checkout is `Social-Media-NLP/` at `4ad1032`. Keep the legacy checkout read-only and verify its status is clean at each gate.

The executor hands off a stable set of artifacts for review, then waits for the verdict before making further edits to those artifacts. Each gate uses one Argon session. The review session disables AI Credits and tool permissions for edits, shell, MCP, and web operations. If Argon is unavailable or its quota is exhausted, hold the gate until it can review.

## Running a gate review

From `/home/drakez/Projects`:

```bash
Social-Media-NLP-Refactor/scripts/review.sh \
  --out Social-Media-NLP-Refactor/reviews/gate-0.md \
  --prompt-file /path/to/gate-0-prompt.md \
  --require-approved
```

The prompt identifies the reviewed paths and the verification evidence. The wrapper uses the shared project directory so Argon can inspect the OpenSpec and both checkouts. It accepts a prompt file or stdin, stages the response before publishing it, and preserves any existing report when the invocation fails or the response lacks a recognized verdict. With `--require-approved`, a saved `CHANGES REQUESTED` verdict returns exit code 3.

## 4. Mandatory Milestone & Review Gates

Every milestone has a blocking exit gate. Advancing past a gate requires a recognized **`APPROVED`** (or **`APPROVED FOR APPLY`**) verdict from `scripts/review.sh` (`--require-approved`). If Argon returns `CHANGES REQUESTED`, Codex resolves every finding, re-runs local verification, and re-runs `scripts/review.sh` until `APPROVED`.

- **Gate 0 — OpenSpec Finalization & Workflow Setup (M0 -> blocks M1)**
  - Resolve review findings and check consistency across `proposal.md`, `design.md`, `tasks.md`, and all 7 `specs/*/spec.md` files.
  - Run `cd /home/drakez/Projects && openspec validate social-media-nlp-refactor --strict` (exit `0`).
  - Run `scripts/review.sh --require-approved` and obtain an explicit **`APPROVED`** verdict from Gemini 4 Argon High.
  - Verify `git -C /home/drakez/Projects/Social-Media-NLP status --porcelain` is empty.

- **Gate 1 — Milestone 1 Local REST-Only Vertical Slice, Playwright E2E & Fair Benchmark (M1 -> blocks M2)**
  - Scoped strictly per Blocker 7: M1-only `goose` tables (`users` with `role`, `is_active`, `last_login_at`; `posts` with `sentiment_type`, `content_version`; `password_reset_tokens`), local email via **Mailpit** (`MailProvider` interface), REST-only backend (`go test -race ./...`), streamlined frontend **without `graphql-request`** and zero legacy libraries (`antd`, `moment`, `date-fns`, `apollo-client`, `socket.io-client`, `rxjs`), repeatable `@playwright/test` E2E (`pnpm test:e2e`), fair Old-vs-New baseline benchmark against equivalent **Milestone 1 common-workload seeded fixtures** (`users` + `posts`) in `docs/ENHANCEMENTS_COMPARISON.md` reporting **API-only RAM and whole-stack RAM separately**, and passing Argon M1 review (`scripts/review.sh --require-approved`).

- **Gate 2 — Milestone 2 Full Legacy Parity, Presigned Media & Realtime (M2 -> blocks M3)**
  - M2 `goose` migrations (including `posts.group_id` FK/index, `users.next_notif_seq`, `chat_rooms.next_seq`, and `notifications.deleted_at` tombstones) & `sqlc` queries, full legacy feature parity (`My Groups`/`Explore`, self-service Leave group + Copy group link, home feed exclusion of group posts via `WHERE group_id IS NULL` introduced in M2, Profile Photos/Videos tabs, group-post sentiment, Admin 24h/7d/rankings/per-user sentiment, state-flipping bookmark toggle + `BOOKMARK` notification + `DELETE /api/bookmarks/:id`, `change-password`, `Shared media`/`Shared links`), serialized conditional staging-to-immutable finalized S3 media (`ETag`/`CopySourceIfMatch` + `FINALIZING` lease/orphaned-object recovery -> `media/verified/{public,private}/<mediaId>/<sha256>`) + strict chat `visibility=PRIVATE` enforcement, all-room `GET /api/chat/rooms/sync` + contiguous reconciled `?afterSeq=` catch-up with deletion tombstones for both `room_seq` and `user_notif_seq`, full-parity `@playwright/test` + `@playwright/mcp` interactive QA fix loop **inside Milestone 2**, and Argon M2 review.

- **Gate 3 — Milestone 3 GraphQL DataLoader & Benchmark-Gated Cloud NLP (M3 -> blocks M4)**
  - `gqlgen` + DataLoader N+1 tests (`graphql-request` introduced on frontend here), `cmd/nlp-eval` bilingual VI+EN macro-F1 benchmark gate, verified active Workers Free models (`@cf/google/gemma-4-26b-a4b-it` primary, `@cf/zai-org/glm-4.7-flash` secondary fallback with measured neuron cost) + shared atomic Valkey Lua UTC daily budget reserving worst-case input+output neurons per attempt (`cf_ai:neurons:YYYY-MM-DD <= 9,000 / 10,000`) across all callers sharing the Cloudflare account + `content_version` race tests for both posts and comments, and Argon M3 review.

- **Gate 4 — Milestone 4 Observability, Kiri K8s HA Lab, DevSecOps CI/CD & $0 No-Card Deploy Gate**
  - VictoriaMetrics + Grafana, Kustomize `local-kiri` + `ha-prod`, `.github/workflows/ci-security.yml` (Gitleaks, Semgrep, `gosec`, `govulncheck`, Trivy + CycloneDX SBOM, Playwright E2E), hosted Resend Free HTTPS adapter (without a verified custom domain, strictly scoped to the **Resend account owner's email address**, returning `503 DEMO_RECIPIENT_RESTRICTED` for third-party emails), **pre-deploy full-stack `@playwright/test` + `@playwright/mcp` interactive QA**, and an enforceable **$0 No-Card Pre-Deploy Gate** (Render auto-deploy disabled; `deploy.yml` triggers Render via API pinned strictly to the exact approved release SHA / immutable image digest only after passing exact-SHA `ci-security.yml`, pre-deploy QA, and `$0 No-Card` provider/payment-method/quota/GitHub-Actions-billing evidence generated `<= 24h` prior) plus Render cold-wake-tolerant (`>= 90s` initial probe) hosted smoke checks, final full-parity `docs/ENHANCEMENTS_COMPARISON.md`, and final Argon review.

---

## 5. Strict $0 No-Card Policy Enforcement

Per `/home/drakez/.config/assistant-memory/AGENTS.md`:
- **Allowed $0 No-Card Runtime Services Only:**
  - Local `kiri compose` / `kiri k8s` learning lab + Mailpit
  - Render Free (single-origin SPA + Go API demo, auto-deploy disabled)
  - Neon Free (PostgreSQL)
  - Upstash Free (Valkey/Redis)
  - Resend Free HTTPS API (without a verified custom domain, delivers strictly to the Resend account owner's email address)
  - Supabase Storage Free (1 GB no-card S3-compatible object storage)
  - Cloudflare Workers AI Free (`<= 10,000` Neurons/day; shared application cap `<= 9,000` via atomic Valkey Lua script)
  - GitHub Actions Free tier (public repository / within free monthly minutes with $0 spending cap)
  - Optional Grafana Cloud Free
- **Strictly Banned Services (unless user explicitly confirms after a cost warning):**
  - Any card-required PaaS/IaaS cloud hosts, overage-billed object storage services, Workers Paid, Billing/Subscriptions, Queues, Images, Stream, Argo, Load Balancing, Registrar/domain purchases, AI Gateway auto top-up, Zero Trust paid.
- **Local-Only Validation:** Do NOT provision live cloud resources in automated local runs; validate infrastructure locally (`kiri compose`, `kiri k8s`, CI/manifest checks) unless explicitly requested by the user.

---

## 6. Shared Memory Protocol (`/home/drakez/.config/assistant-memory/AGENTS.md`)

- The executor updates `/home/drakez/.config/assistant-memory/AGENTS.md` directly to preserve the symlinks at `~/.gemini/AGENTS.md` and `~/.codex/AGENTS.md`.
- Update the `## Social-Media-NLP-Refactor` section at milestone gates or pauses with: key decisions made, verified test/review results, remaining steps, and useful repo paths.
- Never store passwords, PINs, pairing codes, tokens, private keys, or verbatim conversation transcripts.
