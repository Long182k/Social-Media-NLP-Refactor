# Enhancements & Old-vs-New Benchmark Comparison (Milestone 1 Baseline)

This report compares the pinned legacy baseline (`/home/drakez/Projects/Social-Media-NLP`, commit `4ad1032f02fce8cb1e2c8b8c1fe293c6cdf4fdc6`, **NestJS 10 + Prisma + `node-nlp` + Vite/React 18/Ant Design**) against the Milestone 1 vertical slice of `/home/drakez/Projects/Social-Media-NLP-Refactor` (**Go 1.27.4 + `chi` + `pgxpool` + `sqlc` + Vite 8/React 19/Tailwind CSS v4/shadcn/ui**).

All raw measurements are retained verbatim in [`docs/bench-m1-results.json`](./bench-m1-results.json) (captured `2026-11-22T01:12:13Z`, isolated Compose project `smn-bench-29371`).

> **Scope Note (Milestone 1):** Milestone 1 covers the local REST-only vertical slice (auth, same-origin cookie refresh, password reset via Mailpit, local Vietnamese + English 3-label sentiment, and cursor-paginated posts/feed). Full legacy parity (Groups, Events, Media S3, WebSocket chat/notifications, GraphQL DataLoader, Cloud Workers AI, and observability/K8s) is tracked in Milestones 2–4 and will be re-benchmarked in Task 9.1.

---

## 1. Executive Summary (Measured on Equivalent `105 Users / 110 Posts` Fixtures)

| Metric | Legacy @ `4ad1032` | Refactored M1 | Change |
|---|---:|---:|---|
| Cold start, median of 5 trials | `1130.626 ms` (1072.042–1199.608 ms) | `66.002 ms` (59.101–80.728 ms) | `17.13×` speedup (-94.2%) |
| API process RAM, idle | `344.69 MiB` | `44.7 MiB` | `7.71×` speedup (-87.0%) |
| API process RAM, post-load | `395.28 MiB` | `42.93 MiB` | `9.21×` speedup (-89.1%) |
| API process RAM, peak | `395.28 MiB` | `64.62 MiB` | `6.12×` speedup (-83.7%) |
| Whole-stack RAM, idle | `462.46 MiB` | `146.51 MiB` | `3.16×` speedup (-68.3%) |
| Whole-stack RAM, post-load | `549.76 MiB` | `165.18 MiB` | `3.33×` speedup (-70.0%) |
| Feed p50 / p95 / p99, concurrency 10 | `4.267 / 7.131 / 12.068 ms` | `0.956 / 1.703 / 2.583 ms` | `4.46× / 4.19× / 4.67×` legacy / v2 latency |
| Feed throughput, concurrency 10 | `2140.64 RPS` | `9316.07 RPS` | `4.35×` throughput (+335.2%) |
| Feed p50 / p95 / p99, concurrency 1 | `6.143 / 7.494 / 8.139 ms` | `0.81 / 1.304 / 1.57 ms` | `7.58× / 5.75× / 5.18×` legacy / v2 latency |
| Feed throughput, concurrency 1 | `160.39 RPS` | `1126.09 RPS` | `7.02×` throughput (+602.1%) |
| Initial JS, gzip | `1025.46 KiB` | `111.76 KiB` | `9.18×` speedup (-89.1%) |
| Entry JS, gzip | `1025.46 KiB` | `19.45 KiB` | `52.72×` speedup (-98.1%) |
| Total JS, gzip | `1025.46 KiB` | `133.95 KiB` | `7.66×` speedup (-86.9%) |
| CSS, gzip | `18.57 KiB` | `5.83 KiB` | `3.19×` speedup (-68.6%) |
| Total assets, gzip | `3461.26 KiB` | `139.78 KiB` | `24.76×` speedup (-96.0%) |

---

## 2. Test Environment & Reproducible Methodology

### 2.1 Host Hardware & Runtime Versions
- **OS / Kernel:** Linux `7.3.5-2-cachyos` (`x86_64`)
- **CPU:** `Intel(R) Core(TM) i5-14600KF` (`20` hardware threads)
- **System RAM:** `31.16 GiB`
- **Toolchains:** `go version go1.27.4 linux/amd64`, `node v26.13.1`, `pnpm 12.21.0`, `kiri compose` (PostgreSQL `19.0`, Valkey `9.2.0` with `noeviction`)

### 2.2 Reproduction Command
Run from `/home/drakez/Projects/Social-Media-NLP-Refactor` (completely independent of the dev/E2E `deploy/compose.yaml` stack):

```bash
./scripts/bench/bench-m1.sh --json-out docs/bench-m1-results.json
```

The harness executes in a locked isolated Compose project (`smn-bench-<pid>` using `deploy/bench-compose.yaml` on host ports `15432`/`16379` with fresh PostgreSQL `tmpfs` at `/var/lib/postgresql` per leg) without touching the dev/E2E stack or modifying `/home/drakez/Projects/Social-Media-NLP`.

1. Verifies `/home/drakez/Projects/Social-Media-NLP` is clean and `HEAD` equals pinned commit `4ad1032f02fce8cb1e2c8b8c1fe293c6cdf4fdc6`, then exports `BE-Social-Media` and `FE-Social-Media` via `git archive` into a temporary directory (`/tmp/legacy-bench.XXXXXX`, removed unconditionally on `EXIT`) so untracked/ignored files (such as `.env`) cannot enter the benchmark.
2. Brings up fresh isolated `postgres` and `valkey` containers for Phase 1 (**Legacy**), creates `social_media_legacy`, pushes the Prisma schema, seeds `105` users (including `alice@example.com`) and `110` bilingual VI/EN posts (`scripts/bench/seed-legacy.sh`), runs `ANALYZE`, flushes Valkey, and runs cold-start + idle/load/Valkey verification + `node-nlp` measurements. Successful Prisma 5.22.0 schema push, seed, and live profile, login, and feed checks established compatibility with PostgreSQL 19 for this workload.
3. Tears down Phase 1 containers (`down -v`) and brings up fresh isolated `postgres` and `valkey` containers for Phase 2 (**New Go v2**), creating `social_media_v2_bench`, running `goose` migrations, seeding the identical `105` users and `110` posts via `cmd/seed`, running `ANALYZE`, flushing Valkey, and running cold-start + idle/load + NLP measurements.
   - *Cache baseline note:* Recreating fresh `postgres` and `valkey` containers for each stack resets PostgreSQL `shared_buffers`/backend processes, Valkey state, and container cgroup memory baselines cleanly before each stack runs (while Linux host OS kernel page cache remains warm/shared across sequential local runs).
4. Tears down `smn-bench-<pid>` (`down -v --remove-orphans`), removes `/tmp/legacy-bench.*`, writes `docs/bench-m1-results.json`, and asserts `git -C /home/drakez/Projects/Social-Media-NLP status --porcelain` is empty.

### 2.3 Measurement Definitions & Fairness Notes
- **Equivalent Cold-Start Readiness:** Measured via `backend/cmd/benchload -mode=coldstart` from `exec` spawn (`node dist/main` with `IS_PRODUCTION=true` vs pre-compiled `./new-api` binary) until verified Postgres + Argon2 login + exact-JTI Valkey session persistence in every single trial (pre-trial Valkey `FlushDB` verified without error):
  - Legacy (`-probe-mode=legacy-deps -expect-id=<alice_uuid> -valkey-url=...`): polls `GET /api/auth/profile/<alice_uuid>` until `HTTP 200` with matching `id == <alice_uuid>`, `bio != "Fullstack Developer & NLP enthusiast"`, and `len(posts) > 0` (rejecting Legacy's synthetic DB-error fallback), then executes `POST /api/auth/login` (`alice@example.com`/`password`), verifies Postgres-backed `userId == <alice_uuid>`, decodes `jti` from the returned `refreshToken`, and asserts `GET refresh:<alice_uuid>:<jti> == <jti>` in the isolated benchmark Valkey before stopping the trial timer.
  - New (`-probe-mode=v2-deps -expect-id=<alice_uuid> -valkey-url=...`): polls `GET /readyz` (`status == "ready"`, `postgres == "ok"`, `valkey == "ok"`), then executes `POST /api/auth/login` (`alice@example.com`/`password`), verifies `user.id == <alice_uuid>`, decodes `fam` and `jti` from the `refreshToken` cookie, and asserts `GET refresh:family:<fam>` starts with `<alice_uuid>:<jti>:` in the isolated benchmark Valkey before stopping the trial timer.
  - *Node 26 compatibility note:* Legacy `BE-Social-Media` depends transitively on `jsonwebtoken -> jwa@1.4.1 -> buffer-equal-constant-time@1.0.1`, which crashes on Node 26 LTS (`TypeError: Cannot read properties of undefined (reading 'prototype')` because `buffer.SlowBuffer` was removed). The benchmark harness injects a 5-line `NODE_OPTIONS=--require` `SlowBuffer` shim inside `/tmp/legacy-bench.*` solely to keep the untouched legacy code runnable on Node 26.
- **API-process RAM vs. Whole-Stack RAM (explicit separation):**
  - **API-process RAM** reads single-process Linux `/proc/<pid>/status` (`VmRSS` at 2s post-ready idle and post-load, plus `VmHWM` peak RSS).
  - **Whole-Stack RAM** sums `API VmRSS + postgres cgroup MemUsage + valkey cgroup MemUsage` queried strictly by exact full container ID (`bcompose ps -q postgres`, `bcompose ps -q valkey`) via `docker stats --no-stream --no-trunc` (avoiding `ps` RSS double-counting of Postgres `shared_buffers` across backend worker processes and failing hard on any missing or ambiguous sample).
- **Feed benchmark (`GET /api/posts?limit=10`):** Authenticates **once** via `POST /api/auth/login` (`alice@example.com` / `password`), warms up `50` requests, then executes `500` requests at `concurrency=10` and `500` requests at `concurrency=1`, asserting `HTTP 200` and `len(data) == 10` on every response.
  - *Query-shape difference (architectural improvement):* Legacy `PostsService.findAll` runs offset pagination (`SKIP 0 TAKE 10`) plus a separate `SELECT COUNT(*)` and eager-loads `comments`, `attachments`, and `_count`. New `posts.Service.ListPosts` uses keyset cursor pagination `(created_at DESC, id DESC)` with `LIMIT 11` over `idx_posts_created_id_desc` (`sqlc` + `pgx/v5`), eliminating offset drift and `COUNT(*)` scans while returning Milestone 1 `posts` + `PublicUser` author projections.

---

## 3. Detailed Breakdown

### 3.1 API and Whole-Stack Memory (MiB)

| Component | Legacy Idle | Legacy Post-Load | Refactor Idle | Refactor Post-Load |
|---|---:|---:|---:|---:|
| API process RSS | 344.69 | 395.28 | 44.7 | 42.93 |
| API process peak RSS | — | 395.28 | — | 64.62 |
| PostgreSQL cgroup memory | 106.7 | 143.6 | 90.36 | 110.9 |
| Valkey cgroup memory | 11.07 | 10.88 | 11.45 | 11.35 |
| Whole stack | 462.46 | 549.76 | 146.51 | 165.18 |

### 3.2 Cold Start and Feed

- Legacy cold-start trials in ms `[1199.608, 1146.209, 1072.042, 1099.144, 1130.626]`.
- Refactor cold-start trials in ms `[61.214, 80.728, 69.81, 59.101, 66.002]`.

| Feed run | Requests | Errors | Min | Mean | p50 | p95 | p99 | Max | RPS |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| Legacy, c=10 | 500 | 0 | 1.883 | 4.614 | 4.267 | 7.131 | 12.068 | 16.72 | 2140.64 |
| Legacy, c=1 | 500 | 0 | 2.312 | 6.136 | 6.143 | 7.494 | 8.139 | 9.114 | 160.39 |
| Refactor, c=10 | 500 | 0 | 0.18 | 1.011 | 0.956 | 1.703 | 2.583 | 3.525 | 9316.07 |
| Refactor, c=1 | 500 | 0 | 0.29 | 0.835 | 0.81 | 1.304 | 1.57 | 2.092 | 1126.09 |

Feed latency values are in ms. Each run used 50 warmup requests.

### 3.3 Frontend Production Bundle

| Build | Asset | Raw bytes | Gzip bytes |
|---|---|---:|---:|
| Legacy | HTML | 575 | 359 |
| Legacy | Entry JS | 3,480,356 | 1,050,066 |
| Legacy | Preloaded JS | 0 | 0 |
| Legacy | Initial JS | 3,480,356 | 1,050,066 |
| Legacy | Lazy JS | 0 | 0 |
| Legacy | Initial CSS | 96,721 | 19,020 |
| Legacy | All JS | 3,480,356 | 1,050,066 |
| Legacy | All CSS | 96,721 | 19,020 |
| Legacy | All assets | 6,443,827 | 3,544,326 |
| Refactor | HTML | 692 | 374 |
| Refactor | Entry JS | 60,214 | 19,916 |
| Refactor | Preloaded JS | 297,427 | 94,530 |
| Refactor | Initial JS | 357,641 | 114,446 |
| Refactor | Lazy JS | 65,437 | 22,722 |
| Refactor | Initial CSS | 27,526 | 5,969 |
| Refactor | All JS | 423,078 | 137,168 |
| Refactor | All CSS | 27,526 | 5,969 |
| Refactor | All assets | 450,604 | 143,137 |

The bundle checker found zero forbidden legacy dependency markers in the refactored output.

### 3.4 Local Sentiment

Both scorers used the same Vietnamese and English text samples. Each sample had 30 warmup calls and 500 timed calls. The two classification outcomes illustrate language handling on those samples.

| Scorer | Sample | Label | Mean (ms) | p50 (ms) | p95 (ms) | Min (ms) | Max (ms) |
|---|---|---|---:|---:|---:|---:|---:|
| Legacy | Vietnamese | MODERATE (`es` detected) | 0.0053 | 0.0048 | 0.0074 | 0.0043 | 0.0172 |
| Legacy | English | BAD (`en` detected) | 0.0043 | 0.0038 | 0.0056 | 0.0035 | 0.0515 |
| Refactor | Vietnamese | GOOD | 0.0029 | 0.0029 | 0.0031 | 0.0028 | 0.0052 |
| Refactor | English | GOOD | 0.002 | 0.002 | 0.0021 | 0.0017 | 0.0048 |

Initialization measured `38.676 ms` for the legacy `node-nlp` analyzers and `0.013 ms` for the first Go scorer call. The separate Go microbenchmark recorded median `2913.0 ns/op` (VI) and `1728.0 ns/op` (EN).

---

## 4. Milestone 1 Security & Architecture Enhancements

| Area | Legacy (`Social-Media-NLP` @ `4ad1032`) | New (`Social-Media-NLP-Refactor` M1) |
|---|---|---|
| **Secrets & Fail-Fast Config** | Hardcoded JWT fallback secrets (`super_secret_jwt_*`), silent Neon DB fallback if URL lacks `postgresql://` | `config.LoadFromEnv` rejects missing/whitespace/placeholder (including swapped legacy placeholders) secrets at startup (`exit 1`) |
| **User Projections** | Prisma `user: true` / `findUserByKeyword` risks returning `hashedPassword` / `refreshTokenHash` | `sqlc` explicit column projections (`PublicUser`) never select or serialize `password_hash` or `session_epoch` |
| **Registration & Identity** | Case-sensitive username uniqueness vs case-insensitive login lookup; email-shaped usernames allowed | Functional unique indexes on `LOWER(user_name)` and `LOWER(email)` (`409`), `@` disallowed in usernames (`400` + DB `CHECK`), unambiguous `@`-split login query |
| **Refresh Token Security** | Long-lived refresh token returned in JSON & `localStorage`-compatible flows | Same-origin `HttpOnly; SameSite=Lax; Path=/api/auth` cookie, Valkey JTI rotation + idempotent retry on dropped reply + reuse detection (`401` + session family revocation), cookie preserved across transient `503` failures, cross-tab `navigator.locks` + `BroadcastChannel` coordination + transient bootstrap recovery UI |
| **Auth Hardening** | `checkLoginAttempts` throws `401` & swallows Redis errors; `isActive` not enforced across refresh/forgot-password; fallback demo user | Atomic Valkey Lua canonical-account + IP rate limiting (`429` lockout, executed before Argon2id), `isActive` enforced (`403` across login/refresh/forgot-password), durable `session_epoch` bumped atomically on password reset (preserving concurrent post-reset logins), Argon2id hashing, `last_login_at` updated on login/refresh, crypto-random hashed reset tokens via `Mailpit` |
| **Frontend Session & Feed** | `accessToken` + `refreshToken` stored in `localStorage` | In-memory Zustand `accessToken` only; silent `POST /api/auth/refresh` on reload via same-origin cookie; `IntersectionObserver` cursor infinite scroll; zero legacy bloat |
| **Free-Tier ($0 No-Card) Fit** | Legacy API and stack measurements are recorded above | Local benchmark measurements inform sizing. Hosted qualification is part of Milestone 4. |

## 5. Milestone 4 measurements

The [full-parity benchmark](bench-m4-results.json) ran on November 22, 2026, using the same PostgreSQL 19.0 and Valkey 9.2.0 versions and fresh temporary database storage for both legs. Both legs seeded 105 users, 110 home posts, 12 group posts, 12 groups, 24 memberships, 12 events, 24 attendees, 12 comments, 12 likes, 12 bookmarks, 12 follows, 12 chat rooms, 24 participants, 36 messages, 12 notifications, and 36 attachment records. Both load tests exercised the authenticated REST feed. Browser QA covered GraphQL, real uploads and media reads through MinIO.

| Metric | Legacy | Refactor |
|---|---:|---:|
| Cold start median over 5 runs | 1,089.653 ms | 50.910 ms |
| API idle RSS | 355.28 MiB | 47.86 MiB |
| API post-load RSS | 408.14 MiB | 36.33 MiB |
| API, PostgreSQL and Valkey idle RAM | 474.41 MiB | 149.99 MiB |
| API, PostgreSQL and Valkey post-load RAM | 561.12 MiB | 164.03 MiB |
| Feed p50, concurrency 10 | 5.088 ms | 1.293 ms |
| Feed p95, concurrency 10 | 8.706 ms | 2.387 ms |
| Feed p99, concurrency 10 | 14.073 ms | 3.654 ms |
| Feed success, concurrency 10 | 500/500 | 500/500 |
| Feed p50/p95/p99, concurrency 1 | 1.853/3.789/7.079 ms | 0.757/1.686/2.383 ms |
| Initial gzipped JS | 1,025.46 KiB | 115.96 KiB |
| Compressed image layers | 488.39 MB | 7.81 MB |

The legacy Dockerfile encountered Prisma lifecycle errors with the current toolchain. Its archived build used the [measurement patch](../scripts/bench/legacy-image.patch) to generate and stage Prisma explicitly and kept the original Dockerfile's final copy of the builder's Node modules. The Go image contains the API and SPA and has 21.93 MB of uncompressed file content. Image sizes are summed compressed layer bytes from the two BuildKit Docker exports. The patch applies to the copy of `BE-Social-Media` extracted by `git archive` at the recorded commit.

The local monitoring lab reported successful API scrapes in VictoriaMetrics and Grafana. The provisioned Grafana dashboard has eight panels. In one post-QA snapshot, VictoriaMetrics used 73.81 MiB and Grafana used 244.7 MiB under its 256 MiB container limit. Grafana stayed healthy and exceeded the initial estimate of approximately 100 MB. The Kiri Kubernetes lab served 120/120 consecutive readiness requests during an image rollout, and the old pod logged a clean shutdown. A restart of the final image served another 120/120 readiness requests and sent WebSocket code 1001 to an active connection.

### Security audit

Trivy 0.76.2 reported 5 critical and 98 high-severity dependency matches across the archived legacy source lockfiles, compared with zero critical and zero high matches in the refactor source scan. The refactor image scan reported zero high or critical matches and produced a CycloneDX SBOM. The Dockerfile and both rendered Kubernetes overlays passed the high/critical Trivy configuration check. Local Gitleaks, Semgrep, gosec, and govulncheck completed with zero findings. The counts represent the scanner's reported dependency matches.

The refactor adds revision-guarded NLP writes, one-use WebSocket tickets, ordered message and notification recovery, immutable media finalization with owner and visibility checks, contained static-file access, secure production refresh cookies, and a SHA-bound release gate. Local verification used PostgreSQL, Valkey, MinIO, and Mailpit, plus a stub Resend transport. Hosted compatibility and Cloudflare model measurements remain pending provider eligibility.

### Legacy feature coverage

| Area | Refactor behavior | Verification |
|---|---|---|
| Accounts | Register, login, quiet cookie refresh, in-memory access token, signout, reset, password and account management | Chromium, API integration, Mailpit |
| Feed | Post and comment CRUD, cursor search, Vietnamese and English sentiment, likes and state-flipping bookmarks | Chromium, Go revision tests, GraphQL batching test |
| Profiles and social graph | Follow/unfollow, follower directory, profile photos and videos, editable profile and birthday data | Chromium and API integration |
| Groups and events | Explore and My Groups, join approval and leave, copy links, scoped posts, event category listing and RSVP | Chromium and API integration |
| Chat and notifications | Private media, shared links, multi-tab events, read state, ordered catch-up and deletion tombstones | Chromium, Playwright MCP, Go race tests |
| Admin | 24-hour activity, seven-day signups, sentiment rankings, account deactivation and group/event deletion | Chromium and GraphQL authorization tests |
| Media | Presigned upload and finalize, immutable verified public objects, member-only private URLs | Real MinIO tests and Chromium |
| Operations | Same-origin SPA, go/no-go readiness, service metrics, Kiri rollout, gated hosted deployment | Local Compose and Kubernetes, offline release gate tests |

### Video and blog outline

1. Show the legacy dependency graph and the equivalent 105-user fixture, then reproduce the cold-start and RSS rows from the raw JSON.
2. Trace one authenticated feed query from React to GraphQL. Show that a 12-post page runs the same five database queries as a one-post page.
3. Edit a Vietnamese post while a delayed NLP classification is in flight. Show the old result rejected by the version guard and the new local result visible immediately.
4. Open the same conversation in two tabs, send a private attachment, and reconnect one tab to demonstrate sequence catch-up.
5. Show the 120-probe rollout and the eight-panel Grafana dashboard, then walk through the release evidence checks with cloud calls disabled.
