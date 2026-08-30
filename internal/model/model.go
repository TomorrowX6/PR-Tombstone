package model

import "time"

type TombstoneState string

const (
	StateActive           TombstoneState = "ACTIVE"
	StateSuspended        TombstoneState = "SUSPENDED"
	StateSuperseded       TombstoneState = "SUPERSEDED"
	StateInvalidated      TombstoneState = "INVALIDATED"
	StateArchived         TombstoneState = "ARCHIVED"
	StateArchivedAsMerged TombstoneState = "ARCHIVED_AS_MERGED"
)

type Outcome string

const (
	OutcomeSuperseded            Outcome = "superseded"
	OutcomeDuplicate             Outcome = "duplicate"
	OutcomeDesignDisagreement    Outcome = "design_disagreement"
	OutcomeImplementationProblem Outcome = "implementation_problem"
	OutcomePerformanceConcern    Outcome = "performance_concern"
	OutcomeRegressionRisk        Outcome = "regression_risk"
	OutcomeMissingTests          Outcome = "missing_tests"
	OutcomeInsufficientEvidence  Outcome = "insufficient_evidence"
	OutcomeCannotReproduce       Outcome = "cannot_reproduce"
	OutcomeScopeTooLarge         Outcome = "scope_too_large"
	OutcomeUpstreamResolution    Outcome = "upstream_resolution"
	OutcomeInactiveOrAbandoned   Outcome = "inactive_or_abandoned"
	OutcomeNoLongerNeeded        Outcome = "no_longer_needed"
	OutcomeUnknown               Outcome = "unknown"
)

type Repository struct {
	ID             int64  `json:"id"`
	GitHubID       int64  `json:"github_id"`
	InstallationID int64  `json:"installation_id"`
	Owner          string `json:"owner"`
	Name           string `json:"name"`
	Private        bool   `json:"private"`
	TombstoneCount int64  `json:"tombstone_count,omitempty"`
	HighConfidence int64  `json:"high_confidence,omitempty"`
	UnknownReason  int64  `json:"unknown_reason,omitempty"`
}

type RepositorySettings struct {
	RepositoryID    int64  `json:"repository_id"`
	NotifyMode      string `json:"notify_mode"`
	RetentionDays   int    `json:"retention_days"`
	ContentsEnabled bool   `json:"contents_enabled"`
}

type PullRequestSnapshot struct {
	RepositoryID      int64          `json:"repository_id"`
	Number            int            `json:"number"`
	Title             string         `json:"title"`
	Body              string         `json:"body"`
	Author            string         `json:"author"`
	AuthorAssociation string         `json:"author_association"`
	CreatedAt         time.Time      `json:"created_at"`
	ClosedAt          *time.Time     `json:"closed_at,omitempty"`
	BaseBranch        string         `json:"base_branch"`
	HeadBranch        string         `json:"head_branch"`
	Merged            bool           `json:"merged"`
	Labels            []string       `json:"labels"`
	Files             []ChangedFile  `json:"files"`
	Commits           []Commit       `json:"commits"`
	Reviews           []Review       `json:"reviews"`
	Evidence          []EvidenceItem `json:"evidence,omitempty"`
}

type ChangedFile struct {
	Filename  string `json:"filename"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Changes   int    `json:"changes"`
	Patch     string `json:"patch,omitempty"`
}

type Commit struct {
	SHA     string `json:"sha"`
	Message string `json:"message"`
	Author  string `json:"author"`
}

type Review struct {
	ID                int64      `json:"id"`
	State             string     `json:"state"`
	Body              string     `json:"body"`
	Reviewer          string     `json:"reviewer"`
	AuthorAssociation string     `json:"author_association"`
	SubmittedAt       *time.Time `json:"submitted_at,omitempty"`
}

type EvidenceItem struct {
	ID                string    `json:"id"`
	RepositoryID      int64     `json:"repo_id"`
	PRNumber          int       `json:"pr_number"`
	Type              string    `json:"type"`
	Author            string    `json:"author"`
	AuthorAssociation string    `json:"author_association"`
	Path              string    `json:"path,omitempty"`
	Line              int       `json:"line,omitempty"`
	Body              string    `json:"body"`
	SourceURL         string    `json:"source_url"`
	CreatedAt         time.Time `json:"created_at"`
	RankScore         float64   `json:"rank_score,omitempty"`
}

type Claim struct {
	Claim       string   `json:"claim"`
	Confidence  float64  `json:"confidence"`
	EvidenceIDs []string `json:"evidence_ids"`
}

type AnalysisResult struct {
	Summary                        string    `json:"summary"`
	AttemptedApproach              []Claim   `json:"attempted_approach"`
	Outcomes                       []Outcome `json:"outcomes"`
	ValuableFindings               []Claim   `json:"valuable_findings"`
	RejectedOrQuestionedApproaches []Claim   `json:"rejected_or_questioned_approaches"`
	UnresolvedQuestions            []Claim   `json:"unresolved_questions"`
	SuggestedFutureDirection       []Claim   `json:"suggested_future_direction"`
	AffectedAreas                  []string  `json:"affected_areas"`
}

type Tombstone struct {
	ID                             int64               `json:"id"`
	Repository                     Repository          `json:"repository"`
	PR                             PullRequestSnapshot `json:"pull_request"`
	State                          TombstoneState      `json:"state"`
	Summary                        string              `json:"summary"`
	AttemptedApproach              []Claim             `json:"attempted_approach"`
	Outcomes                       []Outcome           `json:"outcomes"`
	ValuableFindings               []Claim             `json:"valuable_findings"`
	RejectedOrQuestionedApproaches []Claim             `json:"rejected_or_questioned_approaches"`
	UnresolvedQuestions            []Claim             `json:"unresolved_questions"`
	SuggestedFutureDirection       []Claim             `json:"suggested_future_direction"`
	AffectedAreas                  []string            `json:"affected_areas"`
	Evidence                       []EvidenceItem      `json:"evidence"`
	Confidence                     float64             `json:"confidence"`
	GeneratedAt                    time.Time           `json:"generated_at"`
	ModelVersion                   string              `json:"model_version"`
	SchemaVersion                  string              `json:"schema_version"`
}

type AnalysisInput struct {
	Snapshot PullRequestSnapshot `json:"snapshot"`
	Evidence []EvidenceItem      `json:"evidence"`
}

type SimilarityMatch struct {
	ID           int64   `json:"id"`
	NewPRNumber  int     `json:"new_pr_number"`
	OldPRNumber  int     `json:"old_pr_number"`
	Score        float64 `json:"score"`
	Relationship string  `json:"relationship"`
	Reason       string  `json:"reason"`
}

type PullRequestHistory struct {
	Number  int               `json:"number"`
	Title   string            `json:"title"`
	Author  string            `json:"author"`
	Matches []SimilarityMatch `json:"matches"`
}

type GraphNode struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Label string `json:"label"`
	URL   string `json:"url,omitempty"`
}

type GraphEdge struct {
	ID         int64  `json:"id"`
	Source     string `json:"source"`
	Target     string `json:"target"`
	Relation   string `json:"relation"`
	EvidenceID string `json:"evidence_id,omitempty"`
}

type DecisionGraph struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

type Installation struct {
	ID           int64      `json:"id"`
	GitHubID     int64      `json:"github_id"`
	AccountLogin string     `json:"account_login"`
	AccountType  string     `json:"account_type"`
	SuspendedAt  *time.Time `json:"suspended_at,omitempty"`
	DeletedAt    *time.Time `json:"deleted_at,omitempty"`
}
