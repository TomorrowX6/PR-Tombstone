-- Dashboard authentication: GitHub OAuth login with server-side sessions and a
-- user-to-installation ACL snapshot. The shared DASHBOARD_TOKEN remains only as
-- a documented self-host fallback; it is never part of the OAuth flow.

CREATE TABLE IF NOT EXISTS dashboard_users (
    id BIGSERIAL PRIMARY KEY,
    github_id BIGINT NOT NULL UNIQUE,
    login TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    avatar_url TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_login_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS dashboard_sessions (
    id BIGSERIAL PRIMARY KEY,
    token_hash TEXT NOT NULL UNIQUE,
    user_id BIGINT NOT NULL REFERENCES dashboard_users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS dashboard_sessions_expiry_idx ON dashboard_sessions (expires_at);

-- Snapshot of the GitHub App installations a user could access at login time,
-- resolved through GET /user/installations with the user's OAuth token. The
-- OAuth token itself is deliberately never persisted; the snapshot refreshes
-- on every login, so access changes propagate on re-login.
CREATE TABLE IF NOT EXISTS user_installations (
    user_id BIGINT NOT NULL REFERENCES dashboard_users(id) ON DELETE CASCADE,
    installation_github_id BIGINT NOT NULL,
    fetched_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, installation_github_id)
);
