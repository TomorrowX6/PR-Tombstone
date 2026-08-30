package repository

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"pr-tombstone/internal/model"
)

// Session expiry is enforced here on every lookup; expired rows are removed
// lazily instead of relying on a background cleaner.

// UpsertDashboardUser inserts or refreshes a dashboard account by GitHub ID.
func (s *Store) UpsertDashboardUser(ctx context.Context, githubID int64, login, name, avatarURL string) (model.DashboardUser, error) {
	var user model.DashboardUser
	err := s.DB.QueryRowContext(ctx, `INSERT INTO dashboard_users (github_id,login,name,avatar_url,last_login_at) VALUES ($1,$2,$3,$4,NOW()) ON CONFLICT (github_id) DO UPDATE SET login=EXCLUDED.login,name=EXCLUDED.name,avatar_url=EXCLUDED.avatar_url,last_login_at=NOW() RETURNING id,github_id,login,name,avatar_url`, githubID, login, name, avatarURL).Scan(&user.ID, &user.GitHubID, &user.Login, &user.Name, &user.AvatarURL)
	return user, err
}

// ReplaceUserInstallations atomically refreshes the user's ACL snapshot.
func (s *Store) ReplaceUserInstallations(ctx context.Context, userID int64, installationIDs []int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_installations WHERE user_id=$1`, userID); err != nil {
		return err
	}
	for _, id := range installationIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_installations (user_id,installation_github_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, userID, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UserInstallations returns the GitHub installation IDs the user may access.
func (s *Store) UserInstallations(ctx context.Context, userID int64) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT installation_github_id FROM user_installations WHERE user_id=$1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// CreateSession persists a session token hash with its expiry.
func (s *Store) CreateSession(ctx context.Context, userID int64, tokenHash string, expiresAt time.Time) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO dashboard_sessions (token_hash,user_id,expires_at) VALUES ($1,$2,$3)`, tokenHash, userID, expiresAt)
	return err
}

// SessionUser resolves a session token hash to the owning user. Expired
// sessions are deleted opportunistically and resolve to sql.ErrNoRows.
func (s *Store) SessionUser(ctx context.Context, tokenHash string) (model.DashboardUser, error) {
	var user model.DashboardUser
	var expiresAt time.Time
	err := s.DB.QueryRowContext(ctx, `SELECT u.id,u.github_id,u.login,u.name,u.avatar_url,d.expires_at FROM dashboard_sessions d JOIN dashboard_users u ON u.id=d.user_id WHERE d.token_hash=$1`, tokenHash).Scan(&user.ID, &user.GitHubID, &user.Login, &user.Name, &user.AvatarURL, &expiresAt)
	if err != nil {
		return model.DashboardUser{}, err
	}
	if time.Now().After(expiresAt) {
		_ = s.DeleteSession(ctx, tokenHash)
		return model.DashboardUser{}, sql.ErrNoRows
	}
	return user, nil
}

// DeleteSession removes a session token hash (logout).
func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM dashboard_sessions WHERE token_hash=$1`, tokenHash)
	return err
}

var ErrForbidden = errors.New("repository is outside the user's accessible installations")

// CheckRepositoryAccess verifies that a repository belongs to one of the
// given installation IDs. A nil filter means unfiltered (self-host modes).
func (s *Store) CheckRepositoryAccess(ctx context.Context, repoID int64, accessibleInstallations []int64) error {
	if accessibleInstallations == nil {
		return nil
	}
	var installationID int64
	err := s.DB.QueryRowContext(ctx, `SELECT installation_id FROM repositories WHERE id=$1`, repoID).Scan(&installationID)
	if errors.Is(err, sql.ErrNoRows) {
		return sql.ErrNoRows
	}
	if err != nil {
		return err
	}
	for _, id := range accessibleInstallations {
		if id == installationID {
			return nil
		}
	}
	return ErrForbidden
}

// CheckTombstoneAccess verifies the tombstone's repository installation is
// accessible. A nil filter means unfiltered (self-host modes).
func (s *Store) CheckTombstoneAccess(ctx context.Context, tombstoneID int64, accessibleInstallations []int64) error {
	if accessibleInstallations == nil {
		return nil
	}
	var installationID int64
	err := s.DB.QueryRowContext(ctx, `SELECT r.installation_id FROM tombstones t JOIN repositories r ON r.id=t.repository_id WHERE t.id=$1`, tombstoneID).Scan(&installationID)
	if errors.Is(err, sql.ErrNoRows) {
		return sql.ErrNoRows
	}
	if err != nil {
		return err
	}
	for _, id := range accessibleInstallations {
		if id == installationID {
			return nil
		}
	}
	return ErrForbidden
}

// installationFilter builds a parameterized IN (…) predicate for the given
// installation column. nil means unfiltered; empty means match nothing.
func installationFilter(column string, ids []int64) (string, []any) {
	if ids == nil {
		return "", nil
	}
	if len(ids) == 0 {
		return " AND " + column + " = -1", nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "$" + strconv.Itoa(i+1)
		args[i] = id
	}
	return " AND " + column + " IN (" + strings.Join(placeholders, ",") + ")", args
}

func repositoryInstallationFilter(ids []int64) (string, []any) {
	return installationFilter("repository.installation_id", ids)
}

func jobInstallationFilter(ids []int64) (string, []any) {
	return installationFilter("installation_id", ids)
}
