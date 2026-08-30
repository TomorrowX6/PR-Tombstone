# Data Retention Policy

| Data | Default retention | Control |
|---|---:|---|
| Raw webhook payload | 24 hours | Fixed maximum; payload is nulled automatically |
| Installation token | Until expiry, in memory only | Never persisted; refreshed shortly before expiry |
| Full diff patch | Until analysis completes | Removed before snapshot and evidence persistence |
| Tombstone, evidence, snapshot, embeddings, relations | 30 days | Per repository: 7, 30, 90 days, or forever |
| Completed job metadata | 90 days | Automatic operational cleanup |
| Failed job metadata | 180 days | Automatic operational cleanup |
| Webhook delivery identifiers | 90 days | Automatic operational cleanup |

The worker performs hourly payload cleanup and retention cleanup. Repository-specific retention overrides the global `RETENTION_DAYS` default. Cascading foreign keys remove embeddings, relations, evidence, snapshots, and matches with repository deletion.
