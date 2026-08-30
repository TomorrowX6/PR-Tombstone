# PR Tombstone

> **Preserve the engineering context behind closed, unmerged pull requests.**

PR Tombstone is a GitHub App that turns closed, unmerged pull requests into durable, searchable engineering records.

A pull request can be closed without being merged for many reasons: an approach may be technically flawed, superseded, incomplete, rejected during review, or simply no longer aligned with the project. The code may disappear from the active development path, but the reasoning behind it can still be valuable.

PR Tombstone preserves that context as structured **Tombstones** containing evidence, review feedback, decisions, failed approaches, and relationships to later work. When a new pull request resembles previously closed work, the system can surface the relevant history before contributors repeat the same dead end.

## Core capabilities

- **Automatic PR archival** — listens for GitHub App webhooks and captures closed, unmerged pull requests together with their description, changed files, commits, reviews, inline comments, conversation comments, and timeline events.
- **Evidence-backed analysis** — combines deterministic extraction with pluggable analyzers (`rules`, `openai`, `openai-compatible`, and `anthropic`). Claims that cannot be linked to real evidence are discarded.
- **Historical similarity search** — combines PostgreSQL full-text search with pgvector-backed semantic ranking. Similarity scores below 60% are hidden, 60–80% are treated as related history, and scores above 80% are surfaced as warnings.
- **Decision graph** — stores 64-dimensional embeddings and typed relations so repositories can build a navigable history of engineering decisions and recurring approaches.
- **Repository dashboard** — React-based interface for repository management, Tombstone search and inspection, historical backfill, related-history exploration, and repository-level settings.
- **Data governance** — supports configurable retention, cascade deletion when installation access is removed, and redaction before patches are persisted.
- **Operational safeguards** — includes transactional embedded migrations, idempotent webhook handling, durable PostgreSQL-backed jobs, exponential retry, structured request logging, health endpoints, and Prometheus metrics.

## How it works

```text
GitHub App webhook
        │ HMAC verification + delivery idempotency
        ▼
Go API ───────────────── PostgreSQL + pgvector
  │                              ▲
  │ enqueue                      │ atomic persistence
  ▼                              │
PostgreSQL job queue ─────── Go worker
                                │
                                ├─ GitHub installation client
                                ├─ evidence ranking + analysis
                                ├─ embeddings + hybrid similarity
                                └─ decision relations

React dashboard ── nginx ── Go API
```

The API and worker run as separate processes from the same modular Go codebase. Webhook requests are authenticated before parsing, work is moved to a durable queue, and analysis is performed asynchronously by the worker.

For new or updated pull requests, PR Tombstone compares the current work against historical Tombstones using semantic similarity together with repository-specific signals such as file overlap, path/module overlap, labels, symbols, and approach similarity.

## Technology stack

| Layer | Technology |
| --- | --- |
| Backend | Go 1.26 |
| Database | PostgreSQL 16 + pgvector |
| Frontend | React 19, TypeScript, Vite 6, TanStack Query |
| Containers | Docker, Docker Compose, distroless backend image, nginx frontend image |
| Deployment | Docker Compose, Kubernetes template, Vercel Go Function + Cron |

## Quick start

### Docker Compose

Create a local environment file and start the stack:

```bash
cp .env.example .env
docker compose up -d --build
```

Then open:

- Dashboard: `http://localhost:5173`
- Readiness: `http://localhost:8080/readyz`
- Metrics: `http://localhost:8080/metrics`

Stop the stack with:

```bash
docker compose down
```

The default configuration uses `AI_PROVIDER=rules` and `EMBEDDING_PROVIDER=local`, so the application can run without credentials from an external model provider.

To connect a real GitHub App, configure the GitHub App credentials and webhook settings described in [docs/GITHUB_APP.md](docs/GITHUB_APP.md).

## Configuration

Configuration is supplied through environment variables. Start with [.env.example](.env.example).

| Variable | Purpose |
| --- | --- |
| `DATABASE_URL` | PostgreSQL/pgvector connection string |
| `HTTP_ADDR` | API listen address |
| `GITHUB_APP_ID` | Numeric GitHub App ID |
| `GITHUB_APP_SLUG` | GitHub App installation-link slug |
| `GITHUB_PRIVATE_KEY` | GitHub App RSA private key; escaped newlines are supported |
| `GITHUB_WEBHOOK_SECRET` | Secret used to verify GitHub webhook signatures |
| `PUBLIC_BASE_URL` | Public dashboard and callback base URL |
| `DASHBOARD_TOKEN` | Optional Bearer token for dashboard and data endpoints |
| `AI_PROVIDER` | Analyzer implementation: `rules`, `openai`, `openai-compatible`, or `anthropic` |
| `AI_BASE_URL` / `AI_API_KEY` / `AI_MODEL` | Analyzer provider configuration |
| `EMBEDDING_PROVIDER` | Embedding implementation: `local` or an OpenAI-compatible endpoint |
| `EMBEDDING_BASE_URL` / `EMBEDDING_API_KEY` / `EMBEDDING_MODEL` | Independent embedding provider configuration |
| `JOB_POLL_INTERVAL` | Worker queue polling interval |
| `RETENTION_DAYS` | Global data-retention period |
| `CRON_SECRET` | Authentication secret for the Vercel Cron worker endpoint |
| `VERCEL_WORKER_BATCH` | Maximum jobs processed by one Vercel worker invocation |
| `VERCEL_WORKER_BUDGET` | Processing time budget for a Vercel worker invocation |
| `DB_MAX_OPEN_CONNS` / `DB_MAX_IDLE_CONNS` | PostgreSQL connection-pool limits |

Analyzer and embedding providers are configured independently so a chat model is not accidentally sent to an embeddings endpoint.

## GitHub App permissions

PR Tombstone is designed around a narrow permission model.

Required repository permissions:

- **Metadata:** read-only
- **Pull requests:** read-only

Optional capabilities are explicitly feature-gated:

- **Contents:** read-only, when repository context is enabled
- **Checks:** read and write, when Check-run notifications are enabled

The application does not require Administration, Actions, or source-code write permissions. See [docs/GITHUB_APP.md](docs/GITHUB_APP.md) for the complete setup procedure and webhook subscriptions.

## API overview

Public operational endpoints include:

```text
GET  /livez
GET  /readyz
GET  /api/healthz
GET  /api/github/install
GET  /api/github/setup
POST /api/github/webhook
```

Repository and Tombstone operations include:

```text
GET      /api/jobs
GET      /api/repositories
GET      /api/repositories/{id}/history
POST     /api/repositories/{id}/backfill
GET|PUT  /api/repositories/{id}/settings
GET      /api/tombstones/repository/{repository_id}
GET      /api/tombstones/{id}
GET      /api/tombstones/{id}/related
POST     /api/tombstones/{id}/reanalyze
PUT      /api/tombstones/{id}/state
GET      /api/graph/repository/{repository_id}
GET      /metrics
```

When `DASHBOARD_TOKEN` is configured, dashboard, data, jobs, and metrics endpoints require `Authorization: Bearer <token>`. Health, GitHub install/setup, and webhook endpoints remain public.

For request parameters and endpoint semantics, see [docs/IMPLEMENTATION.md](docs/IMPLEMENTATION.md).

## Local development

Start only PostgreSQL:

```bash
docker compose up -d postgres
```

Run the API and worker in separate terminals:

```bash
go run ./cmd/server
```

```bash
go run ./cmd/worker
```

Run the frontend development server:

```bash
cd web
npm install
npm run dev
```

The Vite development server is available on `http://localhost:5173` and proxies API requests to the Go backend.

## Deployment

### Docker Compose

`docker-compose.yml` runs PostgreSQL, the API server, the worker, and the web dashboard as one stack. The backend image contains the `server`, `worker`, and `healthcheck` binaries; the frontend image serves the compiled React application through nginx.

### Kubernetes

[deploy/kubernetes.yaml](deploy/kubernetes.yaml) provides a deployment template intended to be paired with PostgreSQL that has pgvector available. Replace the template placeholders before deployment.

### Vercel

The repository also supports a serverless deployment model:

- `api/index.go` serves the API through a Vercel Go Function.
- `web/` is built as the static frontend.
- Vercel Cron invokes `/api/cron/worker` for bounded background job processing.

See [docs/VERCEL.md](docs/VERCEL.md) for database requirements, environment variables, routing, GitHub App callbacks, and deployment-specific considerations.

## Security and data handling

PR Tombstone separates GitHub access from analysis responsibilities. The analyzer receives structured, untrusted evidence and does not receive GitHub credentials.

Webhook signatures are verified over the raw request body before JSON parsing, GitHub delivery IDs are deduplicated, and installation tokens are kept in memory. Full patches are redacted before persistence. Repository access removal triggers the corresponding data cleanup path, and retention is controlled through `RETENTION_DAYS`.

Additional policy documents are available under `docs/`:

- [Security model](docs/SECURITY.md)
- [Privacy policy](docs/PRIVACY.md)
- [Retention policy](docs/RETENTION.md)
- [Deletion policy](docs/DELETION.md)

## Continuous integration

GitHub Actions runs the repository's backend, frontend, and container checks on pushes and pull requests:

- Go formatting verification
- `go vet ./...`
- `go test -race ./...`
- frontend dependency installation and production build
- backend and frontend container builds

The workflow is defined in [.github/workflows/ci.yml](.github/workflows/ci.yml).

## Project structure

```text
.
├── api/                 # Vercel Go Function entry point
├── cmd/
│   ├── server/          # API server
│   ├── worker/          # Queue worker
│   └── healthcheck/     # Container health-check binary
├── internal/
│   ├── analyzer/        # Analysis abstraction and providers
│   ├── confidence/      # Platform confidence scoring
│   ├── config/          # Environment configuration
│   ├── embedding/       # Embedding abstraction and providers
│   ├── evidence/        # Evidence ranking
│   ├── github/          # GitHub App authentication and REST client
│   ├── httpapi/         # HTTP API and middleware
│   ├── ingest/          # Ingestion and analysis pipeline
│   ├── jobs/            # PostgreSQL-backed job processing
│   ├── model/           # Domain models
│   ├── observability/   # Metrics and operational instrumentation
│   ├── repository/      # PostgreSQL persistence and migrations
│   ├── similarity/      # Historical similarity scoring
│   ├── version/         # Version metadata
│   └── webhook/         # Webhook signature verification
├── web/                 # React + Vite dashboard
├── docs/                # Architecture, deployment, security, and policy docs
├── deploy/              # Kubernetes deployment template
├── fixtures/            # GitHub webhook fixture data
├── Dockerfile           # Backend multi-stage image
├── docker-compose.yml   # Local and single-host stack
└── vercel.json          # Vercel build, routing, function, and Cron config
```

## Documentation

| Document | Description |
| --- | --- |
| [Implementation](docs/IMPLEMENTATION.md) | Runtime behavior, configuration, API semantics, analysis, search, and operations |
| [Architecture](docs/ARCHITECTURE.md) | System architecture and process boundaries |
| [GitHub App configuration](docs/GITHUB_APP.md) | App setup, permissions, webhook subscriptions, and runtime values |
| [Vercel deployment](docs/VERCEL.md) | Serverless deployment guide |
| [Security](docs/SECURITY.md) | Security model and threat analysis |
| [Privacy](docs/PRIVACY.md) | Privacy policy |
| [Retention](docs/RETENTION.md) | Data-retention behavior |
| [Deletion](docs/DELETION.md) | Data-deletion behavior |
