# Architecture

```text
GitHub App webhook
        │ HMAC + delivery idempotency
        ▼
Go API ─────────────── PostgreSQL + pgvector
  │                         ▲
  │ enqueue                 │ atomic persistence
  ▼                         │
PostgreSQL job queue ─── Go worker
                            │
                            ├─ GitHub installation client
                            ├─ evidence ranking + analyzer + verifier
                            ├─ embeddings + hybrid similarity
                            └─ decision relations

React dashboard ── nginx ── Go API
```

The API and worker are separate processes built from one modular-monolith codebase. Both use checksum-tracked, advisory-locked migrations. Jobs are claimed with `FOR UPDATE SKIP LOCKED`, recover after stale worker interruption, retry five times with exponential backoff, and then remain visible as failed.

Read and write concerns are separated: the analyzer receives only structured untrusted evidence and no GitHub credentials. The only optional GitHub write is a Check run created by the ingestion service when the repository owner enables it.

