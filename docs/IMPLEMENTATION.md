# PR Tombstone V1.0 implementation

## Local run

The complete stack can be started with:

```powershell
Copy-Item .env.example .env
docker compose up -d --build
go run ./cmd/fixture
```

Open `http://localhost:5173`. The deterministic fixture creates repository `fixture-owner/fixture-repository`, Tombstone PR #18331, evidence, normalized claims, embeddings, and decision relations. Stop the stack with `docker compose down`.

For source-mode development, start PostgreSQL with `docker compose up -d postgres`, run `go run ./cmd/server`, run `go run ./cmd/worker` in a second terminal, and run `npm install; npm run dev` from `web/`.

The default `AI_PROVIDER=rules` analyzer and `EMBEDDING_PROVIDER=local` are deterministic and require no provider credentials.

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `DATABASE_URL` | local PostgreSQL DSN | PostgreSQL/pgvector connection |
| `HTTP_ADDR` | `:8080` | API listen address |
| `GITHUB_APP_ID` | `0` | Numeric GitHub App ID |
| `GITHUB_APP_SLUG` | empty | Install-link slug |
| `GITHUB_PRIVATE_KEY` | empty | RSA private key; escaped newlines are accepted |
| `GITHUB_WEBHOOK_SECRET` | empty | Webhook HMAC secret |
| `GITHUB_API_BASE_URL` | GitHub REST API | API base, overrideable for tests/enterprise |
| `PUBLIC_BASE_URL` | `http://localhost:5173` | Public dashboard/callback base |
| `DASHBOARD_TOKEN` | empty | Optional dashboard Bearer token |
| `AI_PROVIDER` | `rules` | `rules`, `openai`, `openai-compatible`, or `anthropic` |
| `AI_BASE_URL`, `AI_API_KEY`, `AI_MODEL` | empty | Analyzer provider settings |
| `EMBEDDING_PROVIDER` | `local` | `local` or OpenAI-compatible embeddings |
| `EMBEDDING_BASE_URL`, `EMBEDDING_API_KEY`, `EMBEDDING_MODEL` | empty | Independent embedding settings |
| `JOB_POLL_INTERVAL` | `2s` | Worker queue interval |
| `RETENTION_DAYS` | `30` | Global data-retention default |

Analyzer and embedding models are configured independently so a chat model is never accidentally sent to an embeddings endpoint.

## API

Public operational endpoints:

- `GET /livez`
- `GET /readyz`
- `GET /api/healthz` (readiness alias)
- `GET /api/github/install`
- `GET /api/github/setup?installation_id=...&setup_action=...`
- `POST /api/github/webhook`

Dashboard and data endpoints:

- `GET /api/jobs`
- `GET /api/repositories`
- `GET /api/repositories/{id}/history`
- `POST /api/repositories/{id}/backfill?scope=50|100|year|all`
- `GET|PUT /api/repositories/{id}/settings`
- `GET /api/tombstones/repository/{repository_id}?q=...`
- `GET /api/tombstones/{id}`
- `GET /api/tombstones/{id}/related`
- `POST /api/tombstones/{id}/reanalyze`
- `PUT /api/tombstones/{id}/state`
- `GET /api/graph/repository/{repository_id}`
- `GET /metrics`

When `DASHBOARD_TOKEN` is set, pass `Authorization: Bearer TOKEN` to dashboard, data, jobs, and metrics endpoints. The dashboard can store this token in browser local storage. Health, install/setup, and webhook endpoints remain public.

## GitHub processing

The webhook verifies HMAC over the raw body before JSON parsing and deduplicates `X-GitHub-Delivery`. Failed processing releases the delivery for a safe GitHub retry. `closed` unmerged PRs enqueue Tombstone analysis; merged closes archive an existing Tombstone; `reopened` suspends it; `opened` and `synchronize` enqueue historical similarity analysis. Installation and installation-repository events discover selected repositories, update access, and cascade deletion when access is removed.

The installation client caches one-hour tokens in memory, handles pagination up to GitHub's 3,000-file/history cap, respects rate-limit reset and retry headers, and retries transient GET failures. Backfill always queues per-PR work rather than analyzing in the webhook request.

Optional Contents reads collect bounded CODEOWNERS and CONTRIBUTING evidence. Optional Checks writes publish either a successful no-conflict result or neutral historical context. Both capabilities require an explicit repository setting and matching GitHub App permission.

## Analysis and search

1. Deterministic extraction creates typed evidence for PR body, files, commits, reviews, inline comments, conversation comments, timeline events, and optional repository context.
2. Evidence is ranked by source, review state, author association, recency, and explicit decision wording.
3. The configured analyzer returns structured claims.
4. Claims without real evidence IDs are discarded.
5. Platform confidence is recomputed independently.
6. Full patches are redacted before persistence.
7. Four 64-dimensional embeddings and typed graph relations are stored.

Search combines PostgreSQL FTS/fallback text matching with pgvector ranking. New-PR matching combines provider-backed semantic similarity, file overlap, path/module overlap, approach similarity, labels, and symbols. Scores below 60% are hidden; 60–80% are related history; scores above 80% are warnings.

## Operations

Migrations are embedded, checksum tracked, transactional, and serialized with a connection-scoped PostgreSQL advisory lock. Queue claims use `FOR UPDATE SKIP LOCKED`; interrupted running jobs recover after 15 minutes; failures use exponential backoff and stop after five attempts.

Every response receives `X-Request-ID` and a structured request log. `/metrics` exposes request/webhook counters, process gauges, and database-backed queue gauges. `/livez` tests the process and `/readyz` tests PostgreSQL.

Run all static and artifact checks:

```powershell
gofmt -w cmd internal
go vet ./...
go test -race ./...
Push-Location web; npm ci; npm run build; Pop-Location
docker compose config
```

The CI workflow runs these builds and both container builds. `deploy/kubernetes.yaml` is an optional production template; replace its placeholders and use managed PostgreSQL with pgvector.

## Policy and setup documents

- [GitHub App configuration](GITHUB_APP.md)
- [Architecture](ARCHITECTURE.md)
- [Privacy](PRIVACY.md)
- [Retention](RETENTION.md)
- [Deletion](DELETION.md)
- [Security](SECURITY.md)
