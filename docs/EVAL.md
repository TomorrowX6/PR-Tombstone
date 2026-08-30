# Dogfood evaluation harness

The analyzer's outcome vocabulary (superseded, duplicate, performance_concern,
…) is only as trustworthy as its measured precision and recall against real
data. This harness collects real closed-unmerged pull requests, compares the
analyzer's predictions against human labels, and reports multi-label
precision/recall/F1 plus evidence-grounding metrics.

## Workflow

```powershell
go run ./cmd/eval collect -repo owner/name -limit 50   # fetch cases (needs a token)
go run ./cmd/eval label -annotator yourname            # scaffold labels.jsonl
# ... annotate labels.jsonl ...
go run ./cmd/eval run                                   # score and render the report
```

`collect` needs a read-only GitHub token from `GITHUB_TOKEN`,
`GITHUB_EVAL_TOKEN`, or `-token`. The collector lists the newest closed PRs
of the repository, fetches each full snapshot, and saves only the unmerged
ones as cases under `evaldata/cases/`. Each case contains the PR snapshot and
the evidence list exactly as the production pipeline would rank it.

## Dataset layout

```text
evaldata/
├── cases/<owner>__<repo>__<number>.json   # snapshot + ranked evidence
├── labels.jsonl                           # one label per line
└── results/run-<timestamp>.json|.md       # scores + rendered report
```

## Annotation protocol

Annotate each case with the **actual** reason the pull request was closed
without being merged, as stated or clearly implied by the discussion. This is
the ground truth the analyzer is scored against — fill it in from the PR
page, the case JSON, or both.

Rules:

- Choose one or more outcomes from the closed vocabulary (see below). Use
  `unknown` — and only `unknown` — when the available discussion does not
  establish a reason.
- Prefer the reason stated by a maintainer or the PR author over your own
  inference. When maintainers disagree, record every stated reason.
- A PR closed because a later PR subsumes it is `superseded`, not `duplicate`.
  `duplicate` is for two PRs proposing the same change at the same time.
- "Author never came back after review feedback" is `inactive_or_abandoned`;
  "we realized we do not need this at all" is `no_longer_needed`.
- Reviewers demanding tests or benchmarks before merge is `missing_tests`
  (or `insufficient_evidence` when the demand is for measurements that are
  not tests).
- Never label a case you cannot read. Leave it unlabeled — unlabeled cases
  are skipped from every metric.

The `notes` field is free text for edge-case reasoning and is echoed in the
report's per-case appendix.

### Outcome vocabulary

`superseded`, `duplicate`, `design_disagreement`, `implementation_problem`,
`performance_concern`, `regression_risk`, `missing_tests`,
`insufficient_evidence`, `cannot_reproduce`, `scope_too_large`,
`upstream_resolution`, `inactive_or_abandoned`, `no_longer_needed`, `unknown`.

Labels are validated on load: empty lists, values outside this vocabulary,
and duplicates fail loudly rather than silently corrupting the scores.

## Metrics

Outcome scores are computed as **multi-label** classification over the
labeled subset:

- **Micro P/R/F1** pools all (label, class) pairs; **macro P/R/F1** averages
  per-class scores over every class present in either gold or predictions.
- **Exact-set match rate** is the fraction of cases where the predicted
  outcome set equals the gold set exactly.
- **Unknown agreement** counts cases where both gold and prediction are
  exactly `unknown` — the cases where the analyzer honestly reported that the
  evidence establishes no reason.

Two evidence-grounding metrics address "does the prediction rest on real
evidence, or is it a keyword coincidence":

- **Decision coverage** — among cases with a real gold outcome, the fraction
  for which the analyzer produced at least one evidence-backed decision claim
  (`rejected_or_questioned_approaches`). Low coverage means the analyzer is
  falling back to `unknown` or generic statements.
- **Claim grounding** — the fraction of decision claims citing at least one
  human-discussion evidence item (review, review comment, issue comment, or
  timeline event) rather than only the PR body or a diff.

The report also renders a per-case appendix (gold vs predicted vs notes) for
spot-checking systematic confusion between classes.

## Interpretation targets

These are dogfood targets, not acceptance gates:

- The deterministic rules engine should beat the `unknown`-fallback baseline
  on real gold outcomes (micro F1 above the unknown-agreement rate).
- Per-class recall shows which reasons the keyword rules systematically miss;
  each gap is a candidate for a new rule or a prompt improvement.
- Claim grounding below 100% means claims cite only the PR body — a signal
  the claim is inferred rather than grounded in discussion.
