package observability

import (
	"bytes"
	"strings"
	"testing"

	"pr-tombstone/internal/repository"
)

func TestPrometheusMetrics(t *testing.T) {
	metrics := New()
	metrics.ObserveRequest(202, true)
	metrics.ObserveRequest(401, true)
	metrics.ObserveWebhookDuplicate()
	var output bytes.Buffer
	if err := metrics.WritePrometheus(&output, repository.JobStats{Pending: 2, Running: 1, Completed: 4, Failed: 3}); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`pr_tombstone_webhooks_total{outcome="accepted"} 1`, `pr_tombstone_webhooks_total{outcome="rejected"} 1`, `pr_tombstone_analysis_jobs{status="pending"} 2`} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("missing %q in metrics:\n%s", expected, output.String())
		}
	}
}
