# GitHub App configuration

## Required settings

- Webhook URL: `https://HOST/api/github/webhook`
- Setup URL: `https://HOST/api/github/setup`
- Webhook secret: the same value as `GITHUB_WEBHOOK_SECRET`
- Subscribe to `Pull request`, `Installation`, and `Installation repositories` events.

## Minimum repository permissions

- Metadata: read-only
- Pull requests: read-only

Optional permissions are feature-gated:

- Contents: read-only, only when repository context is enabled.
- Checks: read and write, only when `notify_mode=check` is selected.

No Issues permission is required for PR conversation comments or timeline reads. Do not grant Administration, Actions, or source-code write permissions.

## Runtime values

Set `GITHUB_APP_ID`, `GITHUB_APP_SLUG`, `GITHUB_PRIVATE_KEY`, `GITHUB_WEBHOOK_SECRET`, and `PUBLIC_BASE_URL`. The private key may contain literal newlines or escaped `\n` sequences. Installation access tokens are minted from the webhook's installation ID and cached only in memory.
