package eval

import (
	"context"
	"fmt"
	"time"

	"pr-tombstone/internal/evidence"
	"pr-tombstone/internal/github"
)

// Collector fetches real closed-unmerged pull requests from GitHub and
// stores each one as a Case under the dataset directory. It intentionally
// only persists PRs whose Merged flag is false — the population the tombstone
// pipeline is designed for.
type Collector struct {
	Client *github.Client
	Sleep  time.Duration // polite delay between PR fetches; zero means none
}

// CollectStats summarizes one collect run.
type CollectStats struct {
	Listed        int `json:"listed"`
	Fetched       int `json:"fetched"`
	Saved         int `json:"saved"`
	SkippedMerged int `json:"skipped_merged"`
	Failed        int `json:"failed"`
}

// Collect lists the newest closed PRs of a repository and saves the
// unmerged ones as cases. limit bounds the number of saved cases; fetching
// stops early when limit cases are collected or the closed-PR list ends.
func (c Collector) Collect(ctx context.Context, datasetDir, owner, name string, limit int) (CollectStats, error) {
	var stats CollectStats
	// List generously: many closed PRs are merged, so ask for more than the
	// target and stop once the target number of unmerged cases is saved.
	numbers, err := c.Client.ListClosedPullRequestNumbers(ctx, owner, name, limit*4)
	if err != nil {
		return stats, err
	}
	stats.Listed = len(numbers)
	for _, number := range numbers {
		if stats.Saved >= limit {
			break
		}
		if c.Sleep > 0 {
			select {
			case <-time.After(c.Sleep):
			case <-ctx.Done():
				return stats, ctx.Err()
			}
		}
		snapshot, err := c.Client.FetchSnapshot(ctx, owner, name, number, 0)
		stats.Fetched++
		if err != nil {
			stats.Failed++
			continue
		}
		if snapshot.Merged {
			stats.SkippedMerged++
			continue
		}
		collected := Case{
			ID:       fmt.Sprintf("%s__%s__%d", owner, name, number),
			Owner:    owner,
			Name:     name,
			Number:   number,
			URL:      fmt.Sprintf("https://github.com/%s/%s/pull/%d", owner, name, number),
			Snapshot: snapshot,
			Evidence: evidence.Rank(snapshot.Evidence),
		}
		if err := SaveCase(datasetDir, collected); err != nil {
			return stats, err
		}
		stats.Saved++
	}
	return stats, nil
}
