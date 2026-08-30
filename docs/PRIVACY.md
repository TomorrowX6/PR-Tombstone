# Privacy Policy

PR Tombstone processes repository data only to preserve evidence-linked engineering history and surface related prior attempts.

## Data processed

- GitHub App installation and repository identifiers.
- Pull request metadata, files, commits, reviews, review comments, conversation comments, and timeline events.
- Optional CODEOWNERS and CONTRIBUTING files when `contents_enabled` is explicitly enabled.
- Structured Tombstones, similarity matches, embeddings, and decision-graph relations derived from that data.
- Webhook delivery identifiers and raw webhook payloads for short-lived delivery verification.

Installation access tokens remain in process memory and are never stored in the database. Full diff patches are removed after analysis. PR text and source content are treated as untrusted data.

## AI processing

The default `rules` analyzer and `local` embedding provider run locally. If an operator configures an external AI or embedding provider, the minimum ranked evidence required for analysis is sent to that configured provider and its terms apply. PR Tombstone itself does not use repository data to train models.

## Sharing

The application does not sell repository data. Data is available only to the operator, configured infrastructure providers, and AI providers explicitly selected by the operator.

## Controls

Repository owners can choose 7, 30, or 90 day retention, or retain records indefinitely. Uninstalling the GitHub App deletes installation repository data. See [Retention](RETENTION.md) and [Deletion](DELETION.md).

