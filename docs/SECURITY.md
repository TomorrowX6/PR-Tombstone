# Security Policy

## Reporting

Report vulnerabilities through the repository's private security-advisory channel. Include the affected version, reproduction steps, impact, and any relevant logs with credentials removed.

## Security boundaries

- GitHub webhook signatures use HMAC-SHA256 over the raw request body and constant-time comparison.
- Delivery IDs are idempotent and webhook bodies are capped at 10 MiB.
- GitHub App private keys and provider keys are environment or secret-manager inputs and are never returned by an API.
- Installation tokens are cached only in memory and token length or format is never assumed.
- Repository text is untrusted. Analyzer prompts prohibit instruction following, and unsupported evidence IDs are removed after model output.
- Full patches are available only during analysis and are redacted from stored snapshots and evidence responses.
- Dashboard APIs can be protected with constant-time Bearer token validation using `DASHBOARD_TOKEN`.
- Containers run as non-root; the deployment template uses a read-only root filesystem and explicit resource limits.

## Production checklist

- Replace every `TARGET`, `HOST`, `PORT`, and `TOKEN` in deployment configuration.
- Use TLS at the ingress and `sslmode=require` for PostgreSQL.
- Store secrets in the platform secret manager rather than committed manifests.
- Restrict `/metrics` and PostgreSQL to trusted networks.
- Grant only Pull requests: read and Metadata: read by default. Add Contents: read or Checks: write only when the matching repository setting is enabled.
- Rotate the webhook secret, dashboard token, GitHub private key, and AI provider key on exposure.
- Monitor failed jobs, webhook rejection rate, and readiness failures.

