# Deploying PR Tombstone on Vercel

This document describes how to run PR Tombstone as a Vercel project. The
container/local entry points (`cmd/server`, `cmd/worker`, Docker Compose) remain
the primary development path; Vercel is the serverless deployment target.

## Topology

A single Vercel Go Function (`api/index.go`, exported as `Handler`) serves the
entire backend, and the React dashboard in `web/` is deployed as static assets:

- **API requests** — `vercel.json` rewrites `/api/*` to
  `/api/index?__vercel_path=/api/<rest>`. The handler restores the original path
  and dispatches to the same `httpapi.Server` mux used by `cmd/server`, so all
  endpoints (`/api/healthz`, `/api/github/webhook`, `/api/tombstones/...`, ...)
  behave identically.
- **Cron worker** — Vercel Cron calls `GET /api/cron/worker` on the configured
  schedule. The handler authenticates the request and runs
  `jobs.Worker.RunBatch`, a bounded queue drainer, because an infinite polling
  loop is not valid in a request-driven runtime.
- **SPA fallback** — every non-`/api` path that does not match a static file is
  rewritten to `/index.html` so client-side routing works.

Function instances are reused across requests, so the application (database
pool, GitHub App auth, analyzer, embedder) is initialized lazily once per
instance and cached in a package-level singleton. Database migrations run during
that first initialization, serialized against concurrent cold starts.

## Cron worker

`vercel.json` registers:

```json
"crons": [{ "path": "/api/cron/worker", "schedule": "*/5 * * * *" }]
```

Each invocation drains up to `VERCEL_WORKER_BATCH` jobs (default `5`) within
`VERCEL_WORKER_BUDGET` (default `50s`), then recovers stale jobs and performs
hourly housekeeping (payload retention, pruning) via the `maintenance_runs`
ledger. Vercel automatically sends `Authorization: Bearer <CRON_SECRET>` with
cron requests when the `CRON_SECRET` environment variable is set; the handler
rejects unauthenticated calls with `401` and returns `503` until `CRON_SECRET`
is configured. Use a long random value.

Notes:

- The default budget leaves headroom under the 60s function `maxDuration`. On
  Pro you can raise both (`functions.maxDuration` in `vercel.json` and
  `VERCEL_WORKER_BUDGET`) for larger batches.
- Hobby plans only trigger cron jobs once per day. Either upgrade, lower the
  schedule to something you find acceptable, or trigger the endpoint manually
  with `curl -H "Authorization: Bearer $CRON_SECRET" https://<domain>/api/cron/worker`.

## Database

Use a managed PostgreSQL that is reachable from Vercel's serverless functions
(Neon, Supabase, Vercel Postgres, or any provider with a public/pooled endpoint):

- `DATABASE_URL` should use the provider's **pooled** connection string and
  `sslmode=require`.
- pgvector is optional: migrations enable it when available and semantic search
  stays disabled otherwise.
- When the `VERCEL` environment variable is present, the pool defaults shrink to
  `DB_MAX_OPEN_CONNS=4` / `DB_MAX_IDLE_CONNS=1` so concurrently scaled function
  instances do not exhaust the database connection limit. Override both
  variables if your plan allows more.

## Environment variables

Configure these in the Vercel project settings (Production + Preview as needed):

| Variable | Notes |
| --- | --- |
| `DATABASE_URL` | Pooled Postgres DSN with `sslmode=require` |
| `CRON_SECRET` | Long random secret; Vercel Cron sends it as a bearer token |
| `GITHUB_APP_ID` / `GITHUB_PRIVATE_KEY` / `GITHUB_WEBHOOK_SECRET` / `GITHUB_APP_SLUG` | GitHub App credentials |
| `DASHBOARD_TOKEN` | Required to protect the dashboard and non-public API routes |
| `AI_PROVIDER` / `AI_BASE_URL` / `AI_API_KEY` / `AI_MODEL` | Analyzer backend (`rules` works without keys) |
| `EMBEDDING_PROVIDER` / `EMBEDDING_BASE_URL` / `EMBEDDING_API_KEY` / `EMBEDDING_MODEL` | Embedding backend (`local` works without keys) |
| `VERCEL_WORKER_BATCH` / `VERCEL_WORKER_BUDGET` | Optional cron batch tuning |
| `DB_MAX_OPEN_CONNS` / `DB_MAX_IDLE_CONNS` | Optional pool tuning (defaults shrink automatically on Vercel) |
| `RETENTION_DAYS` | Optional; default `30` |

`PUBLIC_BASE_URL` is derived automatically from `VERCEL_PROJECT_PRODUCTION_URL`
or `VERCEL_URL`; set it only for custom domains.

## Deploy steps

1. Provision a managed PostgreSQL and note the pooled DSN.
2. Import the repository into Vercel, keeping the repository root as the project
   root. `vercel.json` pins the build: `web/` dependencies install with
   `npm ci`, the dashboard builds with `npm run build`, and `web/dist` is the
   output directory. The Go function is built from `api/index.go`.
3. Add the environment variables above.
4. Deploy, then verify:
   - `GET https://<domain>/api/healthz` returns `200`.
   - `GET https://<domain>/api/cron/worker` without credentials returns `401`.
   - `GET https://<domain>/` serves the dashboard.
5. Update the GitHub App:
   - Webhook URL → `https://<domain>/api/github/webhook`
   - Homepage/callback URLs → `https://<domain>`
6. Watch the first scheduled (or manual) cron invocation drain queued analysis
   jobs in the Vercel function logs.

### Deployment protection caveat

Vercel Deployment Protection (Vercel Authentication) blocks unauthenticated
requests, which breaks the GitHub webhook. Either disable protection for the
production deployment, or create a *Protection Bypass for Automation* secret and
append `?x-vercel-protection-bypass=<secret>` to the GitHub App webhook URL.
Cron requests are exempt from deployment protection automatically.

## Local verification

```powershell
go build ./...
go vet ./...
go test ./...
cd web; npm ci; npm run build
```

`vercel.json` can be sanity-checked with `npx vercel validate` or by running
`vercel build` / `vercel dev` with the Vercel CLI.
