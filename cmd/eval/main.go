// Command eval is the dogfood evaluation harness. It collects real
// closed-unmerged pull requests, scaffolds a human annotation file, runs the
// configured analyzer over every case, and reports outcome
// precision/recall/F1 plus evidence-grounding metrics.
//
// Usage:
//
//	eval collect -repo owner/name -limit 50 [-dataset ./evaldata] [-token ...]
//	eval label   [-dataset ./evaldata] [-annotator name]
//	eval run     [-dataset ./evaldata]
//
// The GitHub token for collect comes from GITHUB_TOKEN, GITHUB_EVAL_TOKEN or
// -token. See docs/EVAL.md for the annotation protocol and metric definitions.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"pr-tombstone/internal/config"
	"pr-tombstone/internal/eval"
	"pr-tombstone/internal/github"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx := context.Background()
	switch os.Args[1] {
	case "collect":
		runCollect(ctx, os.Args[2:])
	case "label":
		runLabel(os.Args[2:])
	case "run":
		runEvaluate(ctx, os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: eval <collect|label|run> [flags]")
	fmt.Fprintln(os.Stderr, "  collect -repo owner/name -limit N   fetch closed-unmerged PRs as cases")
	fmt.Fprintln(os.Stderr, "  label   -annotator name             scaffold labels.jsonl")
	fmt.Fprintln(os.Stderr, "  run                                 score predictions against labels")
}

func runCollect(ctx context.Context, args []string) {
	flags := flag.NewFlagSet("collect", flag.ExitOnError)
	repo := flags.String("repo", "", "repository as owner/name (required)")
	limit := flags.Int("limit", 50, "number of unmerged cases to collect")
	dataset := flags.String("dataset", "evaldata", "dataset directory")
	token := flags.String("token", "", "GitHub token (defaults to GITHUB_TOKEN / GITHUB_EVAL_TOKEN)")
	sleep := flags.Duration("sleep", 300*time.Millisecond, "delay between PR fetches")
	flags.Parse(args)

	owner, name, ok := strings.Cut(*repo, "/")
	if !ok || owner == "" || name == "" {
		fmt.Fprintln(os.Stderr, "collect: -repo must be owner/name")
		os.Exit(2)
	}
	if *token == "" {
		*token = os.Getenv("GITHUB_EVAL_TOKEN")
	}
	if *token == "" {
		*token = os.Getenv("GITHUB_TOKEN")
	}
	if *token == "" {
		fmt.Fprintln(os.Stderr, "collect: no token — set GITHUB_TOKEN or pass -token")
		os.Exit(2)
	}
	client := github.NewClient("https://api.github.com", *token, nil)
	collector := eval.Collector{Client: client, Sleep: *sleep}
	stats, err := collector.Collect(ctx, *dataset, owner, name, *limit)
	if err != nil {
		slog.Error("collect", "error", err)
		os.Exit(1)
	}
	fmt.Printf("listed %d closed PRs, fetched %d, saved %d cases, skipped %d merged, %d failed\n",
		stats.Listed, stats.Fetched, stats.Saved, stats.SkippedMerged, stats.Failed)
}

func runLabel(args []string) {
	flags := flag.NewFlagSet("label", flag.ExitOnError)
	dataset := flags.String("dataset", "evaldata", "dataset directory")
	annotator := flags.String("annotator", "", "annotator name (defaults to GIT_AUTHOR_NAME or \"annotator\")")
	flags.Parse(args)

	if *annotator == "" {
		*annotator = os.Getenv("GIT_AUTHOR_NAME")
	}
	if *annotator == "" {
		*annotator = "annotator"
	}
	count, err := eval.ScaffoldLabels(*dataset, *annotator)
	if err != nil {
		slog.Error("label", "error", err)
		os.Exit(1)
	}
	fmt.Printf("scaffolded labels.jsonl with %d cases; fill in outcomes per docs/EVAL.md\n", count)
}

func runEvaluate(ctx context.Context, args []string) {
	flags := flag.NewFlagSet("run", flag.ExitOnError)
	dataset := flags.String("dataset", "evaldata", "dataset directory")
	flags.Parse(args)

	cases, err := eval.LoadCases(*dataset)
	if err != nil {
		slog.Error("load cases", "error", err)
		os.Exit(1)
	}
	labels, err := eval.LoadLabels(*dataset)
	if err != nil {
		slog.Error("load labels", "error", err)
		os.Exit(1)
	}
	if len(labels) == 0 {
		slog.Warn("no labels found; scoring will be empty (fill in labels.jsonl first)")
	}
	cfg := config.Load()
	runner := eval.DefaultRunner(cfg.AIProvider, cfg.AIBaseURL, cfg.AIAPIKey, cfg.AIModel)
	report, err := runner.Run(ctx, cases, labels)
	if err != nil {
		slog.Error("run", "error", err)
		os.Exit(1)
	}
	analyzerLabel := cfg.AIProvider
	if cfg.AIModel != "" {
		analyzerLabel = cfg.AIProvider + "/" + cfg.AIModel
	}
	reportPath, err := writeResults(*dataset, report, analyzerLabel)
	if err != nil {
		slog.Error("write results", "error", err)
		os.Exit(1)
	}
	s := report.Summary
	fmt.Printf("cases=%d labeled=%d exact=%.1f%% microF1=%.3f macroF1=%.3f decisionCoverage=%.1f%% claimGrounding=%.1f%%\n",
		s.Cases, s.Labeled, s.ExactMatchRate*100, s.MicroF1, s.MacroF1, s.DecisionCoverage*100, s.ClaimGrounding*100)
	fmt.Printf("report: %s\n", reportPath)
}

func writeResults(datasetDir string, report eval.Report, modelName string) (string, error) {
	dir := filepath.Join(datasetDir, "results")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	jsonPath := filepath.Join(dir, "run-"+stamp+".json")
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(jsonPath, append(data, '\n'), 0o644); err != nil {
		return "", err
	}
	mdPath := filepath.Join(dir, "run-"+stamp+".md")
	if err := os.WriteFile(mdPath, []byte(eval.RenderReport(report, modelName)), 0o644); err != nil {
		return "", err
	}
	return mdPath, nil
}
