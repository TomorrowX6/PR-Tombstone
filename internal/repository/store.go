package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"pr-tombstone/internal/embedding"
	"pr-tombstone/internal/model"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type Store struct{ DB *sql.DB }

type Job struct {
	ID             int64
	RepositoryID   int64
	InstallationID int64
	PRNumber       int
	Kind           string
	Attempts       int
}

type JobStats struct {
	Pending   int64 `json:"pending"`
	Running   int64 `json:"running"`
	Completed int64 `json:"completed"`
	Failed    int64 `json:"failed"`
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	return OpenWithPool(ctx, dsn, 20, 5)
}

func OpenWithPool(ctx context.Context, dsn string, maxOpenConns, maxIdleConns int) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	if maxOpenConns < 1 {
		maxOpenConns = 20
	}
	if maxIdleConns < 0 {
		maxIdleConns = 0
	}
	if maxIdleConns > maxOpenConns {
		maxIdleConns = maxOpenConns
	}
	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(maxIdleConns)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{DB: db}, nil
}

func (s *Store) Close() error { return s.DB.Close() }

func (s *Store) Migrate(ctx context.Context) error {
	// Server and worker may start together. Serialize DDL so CREATE EXTENSION,
	// indexes, and tables cannot race each other during bootstrap.
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock(734129)`); err != nil {
		return err
	}
	defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(734129)`)
	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY, checksum TEXT NOT NULL, applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`); err != nil {
		return err
	}
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		data, err := fs.ReadFile(migrationFiles, "migrations/"+entry.Name())
		if err != nil {
			return err
		}
		digest := sha256.Sum256(data)
		checksum := hex.EncodeToString(digest[:])
		var appliedChecksum string
		err = conn.QueryRowContext(ctx, `SELECT checksum FROM schema_migrations WHERE name=$1`, entry.Name()).Scan(&appliedChecksum)
		if err == nil {
			if appliedChecksum != checksum {
				return fmt.Errorf("migration %s checksum changed after it was applied", entry.Name())
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(data)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", entry.Name(), err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (name,checksum) VALUES ($1,$2)`, entry.Name(), checksum); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %s: %w", entry.Name(), err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func (s *Store) EnsureRepository(ctx context.Context, repo model.Repository) (int64, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx, `INSERT INTO repositories (github_id, installation_id, owner, name, private) VALUES ($1,$2,$3,$4,$5) ON CONFLICT (github_id) DO UPDATE SET installation_id=EXCLUDED.installation_id,owner=EXCLUDED.owner,name=EXCLUDED.name,private=EXCLUDED.private RETURNING id`, repo.GitHubID, repo.InstallationID, repo.Owner, repo.Name, repo.Private).Scan(&id)
	return id, err
}

func (s *Store) UpsertInstallation(ctx context.Context, installation model.Installation) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO installations (github_id,account_login,account_type,suspended_at,deleted_at) VALUES ($1,$2,$3,$4,$5) ON CONFLICT (github_id) DO UPDATE SET account_login=CASE WHEN EXCLUDED.account_login='' THEN installations.account_login ELSE EXCLUDED.account_login END,account_type=CASE WHEN EXCLUDED.account_type='' THEN installations.account_type ELSE EXCLUDED.account_type END,suspended_at=EXCLUDED.suspended_at,deleted_at=EXCLUDED.deleted_at,updated_at=NOW()`, installation.GitHubID, installation.AccountLogin, installation.AccountType, installation.SuspendedAt, installation.DeletedAt)
	return err
}

func (s *Store) SuspendInstallation(ctx context.Context, githubID int64, suspended bool) error {
	if suspended {
		_, err := s.DB.ExecContext(ctx, `UPDATE installations SET suspended_at=NOW(),updated_at=NOW() WHERE github_id=$1`, githubID)
		return err
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE installations SET suspended_at=NULL,updated_at=NOW() WHERE github_id=$1`, githubID)
	return err
}

func (s *Store) DeleteInstallation(ctx context.Context, githubID int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM repositories WHERE installation_id=$1`, githubID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO installations (github_id,deleted_at) VALUES ($1,NOW()) ON CONFLICT (github_id) DO UPDATE SET deleted_at=NOW(),suspended_at=NULL,updated_at=NOW()`, githubID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DeleteRepositoryByGitHubID(ctx context.Context, installationID, githubID int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM repositories WHERE installation_id=$1 AND github_id=$2`, installationID, githubID)
	return err
}

func (s *Store) RecordDelivery(ctx context.Context, deliveryID, event string, payload []byte) (bool, error) {
	result, err := s.DB.ExecContext(ctx, `INSERT INTO webhook_deliveries (delivery_id,event_name,payload) VALUES ($1,$2,$3) ON CONFLICT (delivery_id) DO UPDATE SET event_name=EXCLUDED.event_name,payload=EXCLUDED.payload,received_at=NOW() WHERE webhook_deliveries.processed_at IS NULL AND webhook_deliveries.received_at < NOW()-INTERVAL '5 minutes'`, deliveryID, event, json.RawMessage(payload))
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (s *Store) CompleteDelivery(ctx context.Context, deliveryID string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE webhook_deliveries SET processed_at=NOW() WHERE delivery_id=$1`, deliveryID)
	return err
}

func (s *Store) ReleaseDelivery(ctx context.Context, deliveryID string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM webhook_deliveries WHERE delivery_id=$1 AND processed_at IS NULL`, deliveryID)
	return err
}

func (s *Store) Enqueue(ctx context.Context, repoID, installationID int64, prNumber int, kind string) error {
	_, err := s.EnqueueUnique(ctx, repoID, installationID, prNumber, kind)
	return err
}

func (s *Store) EnqueueUnique(ctx context.Context, repoID, installationID int64, prNumber int, kind string) (bool, error) {
	result, err := s.DB.ExecContext(ctx, `INSERT INTO analysis_jobs (repository_id,installation_id,pr_number,kind) VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`, repoID, installationID, prNumber, kind)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func (s *Store) ClaimJob(ctx context.Context) (*Job, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var job Job
	err = tx.QueryRowContext(ctx, `SELECT id,repository_id,installation_id,pr_number,kind,attempts FROM analysis_jobs WHERE status='pending' AND run_after<=NOW() ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&job.ID, &job.RepositoryID, &job.InstallationID, &job.PRNumber, &job.Kind, &job.Attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE analysis_jobs SET status='running', attempts=attempts+1, updated_at=NOW() WHERE id=$1`, job.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &job, nil
}

func (s *Store) CompleteJob(ctx context.Context, jobID int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE analysis_jobs SET status='completed', updated_at=NOW() WHERE id=$1`, jobID)
	return err
}
func (s *Store) FailJob(ctx context.Context, jobID int64, jobErr error) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE analysis_jobs SET status=CASE WHEN attempts>=5 THEN 'failed' ELSE 'pending' END, last_error=$2, run_after=NOW()+make_interval(secs => LEAST(900, (15 * power(2, GREATEST(attempts-1, 0)))::int)), updated_at=NOW() WHERE id=$1`, jobID, jobErr.Error())
	return err
}

func (s *Store) RecoverStaleJobs(ctx context.Context, staleAfter time.Duration) (int64, error) {
	if staleAfter <= 0 {
		staleAfter = 15 * time.Minute
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE analysis_jobs SET status='pending',last_error=CASE WHEN COALESCE(last_error,'')='' THEN 'recovered after worker interruption' ELSE last_error END,run_after=NOW(),updated_at=NOW() WHERE status='running' AND updated_at < NOW()-make_interval(secs => $1)`, int(staleAfter.Seconds()))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// ClaimMaintenance coordinates periodic cleanup across long-running workers
// and independently scaled serverless invocations.
func (s *Store) ClaimMaintenance(ctx context.Context, name string, interval time.Duration) (bool, error) {
	if name == "" {
		return false, errors.New("maintenance name is required")
	}
	if interval < time.Second {
		interval = time.Second
	}
	var claimed bool
	err := s.DB.QueryRowContext(ctx, `INSERT INTO maintenance_runs (name,ran_at) VALUES ($1,NOW()) ON CONFLICT (name) DO UPDATE SET ran_at=EXCLUDED.ran_at WHERE maintenance_runs.ran_at <= EXCLUDED.ran_at-make_interval(secs => $2) RETURNING TRUE`, name, int64(interval/time.Second)).Scan(&claimed)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return claimed, err
}

// JobStats returns queue counters. Pass a non-nil installation filter to
// scope the counters to the caller's accessible installations (OAuth ACL);
// nil reports the global totals used by self-host modes and Prometheus.
func (s *Store) JobStats(ctx context.Context, onlyInstallations []int64) (JobStats, error) {
	var stats JobStats
	filter, args := jobInstallationFilter(onlyInstallations)
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FILTER (WHERE status='pending'),COUNT(*) FILTER (WHERE status='running'),COUNT(*) FILTER (WHERE status='completed'),COUNT(*) FILTER (WHERE status='failed') FROM analysis_jobs WHERE TRUE`+filter, args...).Scan(&stats.Pending, &stats.Running, &stats.Completed, &stats.Failed)
	return stats, err
}

func (s *Store) SaveAnalysis(ctx context.Context, snapshot model.PullRequestSnapshot, result model.AnalysisResult, ranked []model.EvidenceItem, confidenceValue float64, modelVersion string) error {
	storedSnapshot := redactedSnapshot(snapshot)
	snapshotJSON, err := json.Marshal(storedSnapshot)
	if err != nil {
		return err
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO pull_requests (repository_id,number,title,author,closed_at,merged,snapshot) VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (repository_id,number) DO UPDATE SET title=EXCLUDED.title,author=EXCLUDED.author,closed_at=EXCLUDED.closed_at,merged=EXCLUDED.merged,snapshot=EXCLUDED.snapshot,updated_at=NOW()`, snapshot.RepositoryID, snapshot.Number, snapshot.Title, snapshot.Author, snapshot.ClosedAt, snapshot.Merged, snapshotJSON)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM pr_files WHERE repository_id=$1 AND pr_number=$2`, snapshot.RepositoryID, snapshot.Number)
	if err != nil {
		return err
	}
	for _, file := range snapshot.Files {
		if _, err = tx.ExecContext(ctx, `INSERT INTO pr_files (repository_id,pr_number,filename,status,additions,deletions,changes,patch) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, snapshot.RepositoryID, snapshot.Number, file.Filename, file.Status, file.Additions, file.Deletions, file.Changes, ""); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM pr_commits WHERE repository_id=$1 AND pr_number=$2`, snapshot.RepositoryID, snapshot.Number)
	if err != nil {
		return err
	}
	for _, commit := range snapshot.Commits {
		if _, err = tx.ExecContext(ctx, `INSERT INTO pr_commits (repository_id,pr_number,sha,message,author) VALUES ($1,$2,$3,$4,$5)`, snapshot.RepositoryID, snapshot.Number, commit.SHA, commit.Message, commit.Author); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM evidence_items WHERE repository_id=$1 AND pr_number=$2`, snapshot.RepositoryID, snapshot.Number)
	if err != nil {
		return err
	}
	for _, item := range ranked {
		createdAt := item.CreatedAt
		if createdAt.IsZero() {
			createdAt = snapshot.CreatedAt
		}
		body := item.Body
		if item.Type == "diff" {
			body = ""
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO evidence_items (id,repository_id,pr_number,type,author,author_association,path,line,body,source_url,created_at,rank_score) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, item.ID, item.RepositoryID, item.PRNumber, item.Type, item.Author, item.AuthorAssociation, item.Path, item.Line, body, item.SourceURL, createdAt, item.RankScore); err != nil {
			return err
		}
	}
	state := model.StateActive
	if snapshot.Merged {
		state = model.StateArchivedAsMerged
	}
	var tombstoneID int64
	err = tx.QueryRowContext(ctx, `INSERT INTO tombstones (repository_id,pr_number,state,summary,result,confidence,model_version,schema_version,generated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,'1.0',NOW()) ON CONFLICT (repository_id,pr_number) DO UPDATE SET state=CASE WHEN EXCLUDED.state='ARCHIVED_AS_MERGED' THEN EXCLUDED.state ELSE tombstones.state END,summary=EXCLUDED.summary,result=EXCLUDED.result,confidence=EXCLUDED.confidence,model_version=EXCLUDED.model_version,generated_at=NOW(),updated_at=NOW() RETURNING id`, snapshot.RepositoryID, snapshot.Number, state, result.Summary, resultJSON, confidenceValue, modelVersion).Scan(&tombstoneID)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM tombstone_claims WHERE tombstone_id=$1`, tombstoneID); err != nil {
		return err
	}
	groups := []struct {
		category string
		claims   []model.Claim
	}{
		{"attempted_approach", result.AttemptedApproach},
		{"valuable_findings", result.ValuableFindings},
		{"rejected_or_questioned_approaches", result.RejectedOrQuestionedApproaches},
		{"unresolved_questions", result.UnresolvedQuestions},
		{"suggested_future_direction", result.SuggestedFutureDirection},
	}
	for _, group := range groups {
		for position, claim := range group.claims {
			var claimID int64
			if err := tx.QueryRowContext(ctx, `INSERT INTO tombstone_claims (tombstone_id,category,position,claim,confidence) VALUES ($1,$2,$3,$4,$5) RETURNING id`, tombstoneID, group.category, position, claim.Claim, claim.Confidence).Scan(&claimID); err != nil {
				return err
			}
			seenEvidence := make(map[string]bool)
			for _, evidenceID := range claim.EvidenceIDs {
				if seenEvidence[evidenceID] {
					continue
				}
				seenEvidence[evidenceID] = true
				if _, err := tx.ExecContext(ctx, `INSERT INTO claim_evidence (claim_id,repository_id,pr_number,evidence_id) VALUES ($1,$2,$3,$4)`, claimID, snapshot.RepositoryID, snapshot.Number, evidenceID); err != nil {
					return err
				}
			}
		}
	}
	return tx.Commit()
}

func vectorParam(value []float32) (any, error) {
	if len(value) == 0 {
		return nil, nil
	}
	fitted, err := embedding.Fit(value)
	if err != nil {
		return nil, err
	}
	return embedding.Encode(fitted), nil
}

func (s *Store) SaveEmbeddings(ctx context.Context, tombstoneID int64, values embedding.Set) error {
	params := make([]any, 4)
	for i, value := range [][]float32{values.Title, values.Description, values.Discussion, values.Approach} {
		param, err := vectorParam(value)
		if err != nil {
			return err
		}
		params[i] = param
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO tombstone_embeddings (tombstone_id,title_embedding,description_embedding,discussion_embedding,approach_embedding) VALUES ($1,$2::vector,$3::vector,$4::vector,$5::vector) ON CONFLICT (tombstone_id) DO UPDATE SET title_embedding=EXCLUDED.title_embedding,description_embedding=EXCLUDED.description_embedding,discussion_embedding=EXCLUDED.discussion_embedding,approach_embedding=EXCLUDED.approach_embedding,updated_at=NOW()`, tombstoneID, params[0], params[1], params[2], params[3])
	return err
}

func (s *Store) SearchSemantic(ctx context.Context, repoID int64, queryVector []float32, limit int) ([]model.Tombstone, error) {
	if len(queryVector) == 0 {
		return nil, errors.New("query embedding is empty")
	}
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	fitted, err := embedding.Fit(queryVector)
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT t.id,t.repository_id,t.pr_number,t.state,t.summary,t.result,t.confidence,t.model_version,t.schema_version,t.generated_at,r.owner,r.name,r.github_id,r.installation_id,r.private,p.snapshot FROM tombstones t JOIN tombstone_embeddings e ON e.tombstone_id=t.id JOIN repositories r ON r.id=t.repository_id JOIN pull_requests p ON p.repository_id=t.repository_id AND p.number=t.pr_number WHERE t.repository_id=$1 AND e.approach_embedding IS NOT NULL ORDER BY e.approach_embedding <=> $2::vector LIMIT $3`, repoID, embedding.Encode(fitted), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Tombstone
	for rows.Next() {
		item, scanErr := scanTombstone(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) SemanticScores(ctx context.Context, repoID int64, queryVector []float32, limit int) (map[int]float64, error) {
	if len(queryVector) == 0 {
		return nil, errors.New("query embedding is empty")
	}
	if limit < 1 || limit > 3001 {
		limit = 500
	}
	fitted, err := embedding.Fit(queryVector)
	if err != nil {
		return nil, err
	}
	vector := embedding.Encode(fitted)
	rows, err := s.DB.QueryContext(ctx, `SELECT t.pr_number,GREATEST(COALESCE(1-(e.title_embedding <=> $2::vector),0),COALESCE(1-(e.description_embedding <=> $2::vector),0),COALESCE(1-(e.approach_embedding <=> $2::vector),0)) AS score FROM tombstones t JOIN tombstone_embeddings e ON e.tombstone_id=t.id WHERE t.repository_id=$1 ORDER BY score DESC LIMIT $3`, repoID, vector, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int]float64)
	for rows.Next() {
		var number int
		var score float64
		if err := rows.Scan(&number, &score); err != nil {
			return nil, err
		}
		out[number] = score
	}
	return out, rows.Err()
}

// SaveSnapshotOnly stores a newly opened or synchronized PR before its
// similarity job runs. It deliberately does not create a Tombstone.
func (s *Store) SaveSnapshotOnly(ctx context.Context, snapshot model.PullRequestSnapshot) error {
	storedSnapshot := redactedSnapshot(snapshot)
	snapshotJSON, err := json.Marshal(storedSnapshot)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO pull_requests (repository_id,number,title,author,closed_at,merged,snapshot) VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (repository_id,number) DO UPDATE SET title=EXCLUDED.title,author=EXCLUDED.author,closed_at=EXCLUDED.closed_at,merged=EXCLUDED.merged,snapshot=EXCLUDED.snapshot,updated_at=NOW()`, snapshot.RepositoryID, snapshot.Number, snapshot.Title, snapshot.Author, snapshot.ClosedAt, snapshot.Merged, snapshotJSON)
	return err
}

func redactedSnapshot(snapshot model.PullRequestSnapshot) model.PullRequestSnapshot {
	stored := snapshot
	stored.Files = append([]model.ChangedFile(nil), snapshot.Files...)
	for i := range stored.Files {
		stored.Files[i].Patch = ""
	}
	stored.Evidence = nil
	return stored
}

func (s *Store) SetTombstoneState(ctx context.Context, repoID int64, prNumber int, state model.TombstoneState) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE tombstones SET state=$3,updated_at=NOW() WHERE repository_id=$1 AND pr_number=$2`, repoID, prNumber, state)
	return err
}

func (s *Store) SetTombstoneStateByID(ctx context.Context, id int64, state model.TombstoneState) error {
	switch state {
	case model.StateActive, model.StateSuspended, model.StateSuperseded, model.StateInvalidated, model.StateArchived:
	default:
		return errors.New("invalid tombstone state")
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE tombstones SET state=$2,updated_at=NOW() WHERE id=$1`, id, state)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) ListRepositories(ctx context.Context, onlyInstallations []int64) ([]model.Repository, error) {
	filter, args := repositoryInstallationFilter(onlyInstallations)
	rows, err := s.DB.QueryContext(ctx, `SELECT repository.id,repository.github_id,repository.installation_id,repository.owner,repository.name,repository.private,COUNT(tombstone.id),COUNT(tombstone.id) FILTER (WHERE tombstone.confidence>=0.75),COUNT(tombstone.id) FILTER (WHERE tombstone.result->'outcomes' @> '["unknown"]'::jsonb) FROM repositories repository LEFT JOIN tombstones tombstone ON tombstone.repository_id=repository.id WHERE TRUE`+filter+` GROUP BY repository.id ORDER BY repository.owner,repository.name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Repository
	for rows.Next() {
		var item model.Repository
		if err := rows.Scan(&item.ID, &item.GitHubID, &item.InstallationID, &item.Owner, &item.Name, &item.Private, &item.TombstoneCount, &item.HighConfidence, &item.UnknownReason); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) ListTombstones(ctx context.Context, repoID int64) ([]model.Tombstone, error) {
	return s.ListTombstonesPage(ctx, repoID, 3000, 0)
}

func (s *Store) ListTombstonesPage(ctx context.Context, repoID int64, limit, offset int) ([]model.Tombstone, error) {
	if limit < 1 || limit > 3001 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT t.id,t.repository_id,t.pr_number,t.state,t.summary,t.result,t.confidence,t.model_version,t.schema_version,t.generated_at,r.owner,r.name,r.github_id,r.installation_id,r.private,p.snapshot FROM tombstones t JOIN repositories r ON r.id=t.repository_id JOIN pull_requests p ON p.repository_id=t.repository_id AND p.number=t.pr_number WHERE t.repository_id=$1 ORDER BY t.generated_at DESC LIMIT $2 OFFSET $3`, repoID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Tombstone
	for rows.Next() {
		item, err := scanTombstone(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) GetTombstone(ctx context.Context, id int64) (model.Tombstone, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT t.id,t.repository_id,t.pr_number,t.state,t.summary,t.result,t.confidence,t.model_version,t.schema_version,t.generated_at,r.owner,r.name,r.github_id,r.installation_id,r.private,p.snapshot FROM tombstones t JOIN repositories r ON r.id=t.repository_id JOIN pull_requests p ON p.repository_id=t.repository_id AND p.number=t.pr_number WHERE t.id=$1`, id)
	return scanTombstone(row)
}

type scanner interface{ Scan(...any) error }

func scanTombstone(row scanner) (model.Tombstone, error) {
	var t model.Tombstone
	var repoID, prNumber int64
	var state string
	var resultJSON, snapshotJSON []byte
	var repo model.Repository
	if err := row.Scan(&t.ID, &repoID, &prNumber, &state, &t.Summary, &resultJSON, &t.Confidence, &t.ModelVersion, &t.SchemaVersion, &t.GeneratedAt, &repo.Owner, &repo.Name, &repo.GitHubID, &repo.InstallationID, &repo.Private, &snapshotJSON); err != nil {
		return t, err
	}
	repo.ID = repoID
	t.Repository = repo
	if err := json.Unmarshal(snapshotJSON, &t.PR); err != nil {
		return t, err
	}
	var result model.AnalysisResult
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		return t, err
	}
	t.AttemptedApproach = result.AttemptedApproach
	t.Outcomes = result.Outcomes
	t.ValuableFindings = result.ValuableFindings
	t.RejectedOrQuestionedApproaches = result.RejectedOrQuestionedApproaches
	t.UnresolvedQuestions = result.UnresolvedQuestions
	t.SuggestedFutureDirection = result.SuggestedFutureDirection
	t.AffectedAreas = result.AffectedAreas
	t.PR.RepositoryID = repoID
	t.PR.Number = int(prNumber)
	t.PR.Evidence = nil
	for i := range t.PR.Files {
		t.PR.Files[i].Patch = ""
	}
	t.State = model.TombstoneState(state)
	return t, nil
}

func (s *Store) RepositoryForJob(ctx context.Context, job *Job) (model.Repository, error) {
	var repo model.Repository
	err := s.DB.QueryRowContext(ctx, `SELECT id,github_id,installation_id,owner,name,private FROM repositories WHERE id=$1`, job.RepositoryID).Scan(&repo.ID, &repo.GitHubID, &repo.InstallationID, &repo.Owner, &repo.Name, &repo.Private)
	return repo, err
}

func (s *Store) GetRepository(ctx context.Context, id int64) (model.Repository, error) {
	var repo model.Repository
	err := s.DB.QueryRowContext(ctx, `SELECT id,github_id,installation_id,owner,name,private FROM repositories WHERE id=$1`, id).Scan(&repo.ID, &repo.GitHubID, &repo.InstallationID, &repo.Owner, &repo.Name, &repo.Private)
	return repo, err
}

func (s *Store) GetSettings(ctx context.Context, repoID int64) (model.RepositorySettings, error) {
	var settings model.RepositorySettings
	settings.RepositoryID = repoID
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO repository_settings (repository_id) VALUES ($1) ON CONFLICT (repository_id) DO NOTHING`, repoID); err != nil {
		return settings, err
	}
	err := s.DB.QueryRowContext(ctx, `SELECT repository_id,notify_mode,retention_days,contents_enabled FROM repository_settings WHERE repository_id=$1`, repoID).Scan(&settings.RepositoryID, &settings.NotifyMode, &settings.RetentionDays, &settings.ContentsEnabled)
	return settings, err
}

func (s *Store) UpdateSettings(ctx context.Context, settings model.RepositorySettings) error {
	if settings.NotifyMode != "dashboard" && settings.NotifyMode != "check" {
		return errors.New("notify_mode must be dashboard or check")
	}
	if settings.RetentionDays != 0 && settings.RetentionDays != 7 && settings.RetentionDays != 30 && settings.RetentionDays != 90 && settings.RetentionDays != -1 {
		return errors.New("retention_days must be 7, 30, 90, or -1")
	}
	if settings.RetentionDays == 0 {
		settings.RetentionDays = 30
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO repository_settings (repository_id,notify_mode,retention_days,contents_enabled) VALUES ($1,$2,$3,$4) ON CONFLICT (repository_id) DO UPDATE SET notify_mode=EXCLUDED.notify_mode,retention_days=EXCLUDED.retention_days,contents_enabled=EXCLUDED.contents_enabled,updated_at=NOW()`, settings.RepositoryID, settings.NotifyMode, settings.RetentionDays, settings.ContentsEnabled)
	return err
}

func (s *Store) TombstoneIDForPR(ctx context.Context, repoID int64, prNumber int) (int64, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM tombstones WHERE repository_id=$1 AND pr_number=$2`, repoID, prNumber).Scan(&id)
	return id, err
}

func (s *Store) ListEvidence(ctx context.Context, repoID int64, prNumber int) ([]model.EvidenceItem, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,type,author,author_association,COALESCE(path,''),COALESCE(line,0),body,source_url,COALESCE(created_at,NOW()),rank_score FROM evidence_items WHERE repository_id=$1 AND pr_number=$2 ORDER BY rank_score DESC,created_at DESC`, repoID, prNumber)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.EvidenceItem
	for rows.Next() {
		var item model.EvidenceItem
		item.RepositoryID = repoID
		item.PRNumber = prNumber
		if err := rows.Scan(&item.ID, &item.Type, &item.Author, &item.AuthorAssociation, &item.Path, &item.Line, &item.Body, &item.SourceURL, &item.CreatedAt, &item.RankScore); err != nil {
			return nil, err
		}
		if item.Type == "diff" {
			item.Body = ""
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) DeleteExpiredPayloads(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE webhook_deliveries SET payload=NULL WHERE received_at < NOW() - INTERVAL '24 hours'`)
	return err
}

func (s *Store) PruneExpiredData(ctx context.Context, defaultDays int) error {
	if defaultDays < 1 {
		defaultDays = 30
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM decision_relations relation USING repositories repository LEFT JOIN repository_settings settings ON settings.repository_id=repository.id WHERE relation.repository_id=repository.id AND COALESCE(settings.retention_days,$1) <> -1 AND relation.created_at < NOW() - (COALESCE(settings.retention_days,$1)::text || ' days')::interval`, defaultDays); err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM tombstones t USING repositories r LEFT JOIN repository_settings settings ON settings.repository_id=r.id WHERE t.repository_id=r.id AND COALESCE(settings.retention_days,$1) <> -1 AND t.updated_at < NOW() - (COALESCE(settings.retention_days,$1)::text || ' days')::interval`, defaultDays); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM pull_requests p USING repositories r LEFT JOIN repository_settings settings ON settings.repository_id=r.id WHERE p.repository_id=r.id AND COALESCE(settings.retention_days,$1) <> -1 AND p.updated_at < NOW() - (COALESCE(settings.retention_days,$1)::text || ' days')::interval`, defaultDays)
	return err
}

func (s *Store) PruneOperationalData(ctx context.Context) error {
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM webhook_deliveries WHERE received_at < NOW()-INTERVAL '90 days'`); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM analysis_jobs WHERE (status='completed' AND updated_at < NOW()-INTERVAL '90 days') OR (status='failed' AND updated_at < NOW()-INTERVAL '180 days')`)
	return err
}

func (s *Store) SearchTombstones(ctx context.Context, repoID int64, query string) ([]model.Tombstone, error) {
	return s.SearchTombstonesPage(ctx, repoID, query, 3000, 0)
}

func (s *Store) SearchTombstonesPage(ctx context.Context, repoID int64, query string, limit, offset int) ([]model.Tombstone, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return s.ListTombstonesPage(ctx, repoID, limit, offset)
	}
	if limit < 1 || limit > 3000 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT t.id,t.repository_id,t.pr_number,t.state,t.summary,t.result,t.confidence,t.model_version,t.schema_version,t.generated_at,r.owner,r.name,r.github_id,r.installation_id,r.private,p.snapshot FROM tombstones t JOIN repositories r ON r.id=t.repository_id JOIN pull_requests p ON p.repository_id=t.repository_id AND p.number=t.pr_number WHERE t.repository_id=$1 AND (to_tsvector('english', t.summary || ' ' || t.result::text) @@ plainto_tsquery('english',$2) OR t.summary ILIKE '%'||$2||'%' OR t.result::text ILIKE '%'||$2||'%') ORDER BY t.generated_at DESC LIMIT $3 OFFSET $4`, repoID, query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Tombstone
	for rows.Next() {
		item, err := scanTombstone(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) SaveSimilarityMatches(ctx context.Context, repoID int64, newPRNumber int, matches []model.SimilarityMatch) error {
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM similarity_matches WHERE repository_id=$1 AND new_pr_number=$2`, repoID, newPRNumber); err != nil {
		return err
	}
	for _, match := range matches {
		components, err := json.Marshal(match.Components)
		if err != nil {
			return err
		}
		if _, err := s.DB.ExecContext(ctx, `INSERT INTO similarity_matches (repository_id,new_pr_number,old_pr_number,score,relationship,reason,components) VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (repository_id,new_pr_number,old_pr_number) DO UPDATE SET score=EXCLUDED.score,relationship=EXCLUDED.relationship,reason=EXCLUDED.reason,components=EXCLUDED.components,created_at=NOW()`, repoID, match.NewPRNumber, match.OldPRNumber, match.Score, match.Relationship, match.Reason, components); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) UpsertDecisionRelations(ctx context.Context, repoID int64, snapshot model.PullRequestSnapshot, tombstoneID int64, analysis *model.AnalysisResult, matches []model.SimilarityMatch) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	add := func(sourceType, sourceKey, relation, targetType, targetKey, evidenceID string, metadata map[string]any) error {
		data, _ := json.Marshal(metadata)
		_, err := tx.ExecContext(ctx, `INSERT INTO decision_relations (repository_id,source_type,source_key,relation,target_type,target_key,evidence_id,metadata) VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (repository_id,source_type,source_key,relation,target_type,target_key) DO UPDATE SET evidence_id=EXCLUDED.evidence_id,metadata=EXCLUDED.metadata,created_at=NOW()`, repoID, sourceType, sourceKey, relation, targetType, targetKey, evidenceID, data)
		return err
	}
	prKey := fmt.Sprintf("pr:%d", snapshot.Number)
	tombstoneKey := fmt.Sprintf("tombstone:%d", tombstoneID)
	claimPrefix := fmt.Sprintf("claim:%d:", tombstoneID)
	if _, err := tx.ExecContext(ctx, `DELETE FROM decision_relations WHERE repository_id=$1 AND (source_key=$2 OR source_key LIKE $3 OR (source_key=$4 AND relation='has_tombstone'))`, repoID, tombstoneKey, claimPrefix+"%", prKey); err != nil {
		return err
	}
	if err := add("repository", fmt.Sprintf("repo:%d", repoID), "contains_pr", "pull_request", prKey, "", nil); err != nil {
		return err
	}
	if err := add("pull_request", prKey, "has_tombstone", "tombstone", tombstoneKey, "", nil); err != nil {
		return err
	}
	for _, item := range snapshot.Evidence {
		if err := add("tombstone", tombstoneKey, "supported_by", "evidence", "evidence:"+item.ID, item.ID, nil); err != nil {
			return err
		}
	}
	for _, file := range snapshot.Files {
		if err := add("tombstone", tombstoneKey, "affects_path", "path", file.Filename, "", map[string]any{"status": file.Status}); err != nil {
			return err
		}
	}
	if analysis != nil {
		groups := []struct {
			category string
			claims   []model.Claim
		}{
			{"attempted_approach", analysis.AttemptedApproach},
			{"valuable_findings", analysis.ValuableFindings},
			{"rejected_or_questioned_approaches", analysis.RejectedOrQuestionedApproaches},
			{"unresolved_questions", analysis.UnresolvedQuestions},
			{"suggested_future_direction", analysis.SuggestedFutureDirection},
		}
		for _, group := range groups {
			for position, claim := range group.claims {
				claimKey := fmt.Sprintf("claim:%d:%s:%d", tombstoneID, group.category, position)
				if err := add("tombstone", tombstoneKey, "has_claim", "claim", claimKey, "", map[string]any{"claim": claim.Claim, "confidence": claim.Confidence}); err != nil {
					return err
				}
				for _, evidenceID := range claim.EvidenceIDs {
					if err := add("claim", claimKey, "supported_by", "evidence", "evidence:"+evidenceID, evidenceID, nil); err != nil {
						return err
					}
				}
			}
		}
	}
	for _, match := range matches {
		if err := add("pull_request", prKey, "similar_to", "pull_request", fmt.Sprintf("pr:%d", match.OldPRNumber), "", map[string]any{"score": match.Score, "relationship": match.Relationship}); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) UpsertSimilarityRelations(ctx context.Context, repoID int64, snapshot model.PullRequestSnapshot, matches []model.SimilarityMatch) error {
	for _, match := range matches {
		metadata, _ := json.Marshal(map[string]any{"score": match.Score, "relationship": match.Relationship})
		if _, err := s.DB.ExecContext(ctx, `INSERT INTO decision_relations (repository_id,source_type,source_key,relation,target_type,target_key,metadata) VALUES ($1,'pull_request',$2,'similar_to','pull_request',$3,$4) ON CONFLICT (repository_id,source_type,source_key,relation,target_type,target_key) DO UPDATE SET metadata=EXCLUDED.metadata,created_at=NOW()`, repoID, fmt.Sprintf("pr:%d", snapshot.Number), fmt.Sprintf("pr:%d", match.OldPRNumber), metadata); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) DecisionGraph(ctx context.Context, repoID int64) (model.DecisionGraph, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,source_type,source_key,relation,target_type,target_key,COALESCE(evidence_id,'') FROM decision_relations WHERE repository_id=$1 ORDER BY id`, repoID)
	if err != nil {
		return model.DecisionGraph{}, err
	}
	defer rows.Close()
	graph := model.DecisionGraph{}
	seen := map[string]bool{}
	for rows.Next() {
		var edge model.GraphEdge
		var sourceType, sourceKey, targetType, targetKey string
		if err := rows.Scan(&edge.ID, &sourceType, &sourceKey, &edge.Relation, &targetType, &targetKey, &edge.EvidenceID); err != nil {
			return graph, err
		}
		edge.Source = sourceKey
		edge.Target = targetKey
		graph.Edges = append(graph.Edges, edge)
		for _, node := range []model.GraphNode{{ID: sourceKey, Type: sourceType, Label: graphLabel(sourceType, sourceKey)}, {ID: targetKey, Type: targetType, Label: graphLabel(targetType, targetKey)}} {
			if !seen[node.ID] {
				seen[node.ID] = true
				graph.Nodes = append(graph.Nodes, node)
			}
		}
	}
	return graph, rows.Err()
}

func graphLabel(nodeType, key string) string {
	switch nodeType {
	case "repository":
		return "Repository " + strings.TrimPrefix(key, "repo:")
	case "pull_request":
		return "PR #" + strings.TrimPrefix(key, "pr:")
	case "tombstone":
		return "Tombstone #" + strings.TrimPrefix(key, "tombstone:")
	case "evidence":
		return "Evidence " + strings.TrimPrefix(key, "evidence:")
	case "path":
		return key
	case "claim":
		parts := strings.Split(key, ":")
		if len(parts) >= 4 {
			return strings.ReplaceAll(parts[2], "_", " ") + " #" + parts[len(parts)-1]
		}
		return "Claim"
	default:
		return key
	}
}

func (s *Store) ListSimilarityMatches(ctx context.Context, repoID int64, newPRNumber int) ([]model.SimilarityMatch, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,new_pr_number,old_pr_number,score,relationship,reason,components FROM similarity_matches WHERE repository_id=$1 AND new_pr_number=$2 ORDER BY score DESC`, repoID, newPRNumber)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.SimilarityMatch
	for rows.Next() {
		var item model.SimilarityMatch
		var components []byte
		if err := rows.Scan(&item.ID, &item.NewPRNumber, &item.OldPRNumber, &item.Score, &item.Relationship, &item.Reason, &components); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(components, &item.Components); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) ListOpenPRHistory(ctx context.Context, repoID int64) ([]model.PullRequestHistory, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT number,title,author FROM pull_requests WHERE repository_id=$1 AND closed_at IS NULL AND merged=FALSE ORDER BY updated_at DESC LIMIT 100`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.PullRequestHistory
	for rows.Next() {
		var item model.PullRequestHistory
		if err := rows.Scan(&item.Number, &item.Title, &item.Author); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		matches, err := s.ListSimilarityMatches(ctx, repoID, out[i].Number)
		if err != nil {
			return nil, err
		}
		out[i].Matches = matches
	}
	return out, nil
}
