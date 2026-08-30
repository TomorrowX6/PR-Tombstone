# Deletion Policy

PR Tombstone handles the GitHub `installation.deleted` webhook as the authoritative uninstall signal.

1. The installation can no longer mint usable access tokens.
2. All repositories associated with the installation are deleted in one database transaction.
3. Cascading foreign keys delete pull request snapshots, jobs, evidence, Tombstones, embeddings, similarity matches, settings, and decision relations.
4. The installation row is retained only as a deleted lifecycle marker and contains no repository content.
5. Raw webhook payload content is removed by the 24-hour payload cleanup.

Operators can also execute the same repository deletion transaction in response to a verified owner request. Database backups must follow the operator's documented backup expiry schedule; deleted data may remain in encrypted backups until that schedule expires.

