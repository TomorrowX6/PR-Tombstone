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

## OAuth app for dashboard login

The dashboard login flow uses a separate OAuth App registered under the
GitHub App's account (GitHub Apps embed an OAuth identity; configure its
"Identifying and authorizing users" section):

- Callback URL: `https://HOST/api/auth/callback`
- Copy the client ID and a generated client secret into
  `GITHUB_OAUTH_CLIENT_ID` / `GITHUB_OAUTH_CLIENT_SECRET`.

The OAuth app needs no repository permissions. At login the user token is
used once to read the account profile and `GET /user/installations`; the
resulting installation IDs become the user's repository ACL. The token is
never stored server-side — the ACL snapshot refreshes on every login.
Sessions are opaque tokens held in an HttpOnly cookie whose SHA-256 hash is
the only server-side value.
