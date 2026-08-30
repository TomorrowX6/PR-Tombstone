package observability

import (
	"fmt"
	"io"
	"runtime"
	"sync/atomic"
	"time"

	"pr-tombstone/internal/repository"
	"pr-tombstone/internal/version"
)

// Metrics is a dependency-free Prometheus exposition surface for the small
// modular monolith. Database-backed queue gauges are supplied at render time.
type Metrics struct {
	started           time.Time
	requests          atomic.Uint64
	clientErrors      atomic.Uint64
	serverErrors      atomic.Uint64
	webhookAccepted   atomic.Uint64
	webhookRejected   atomic.Uint64
	webhookDuplicates atomic.Uint64
}

func New() *Metrics { return &Metrics{started: time.Now()} }

func (m *Metrics) ObserveRequest(status int, webhook bool) {
	m.requests.Add(1)
	if status >= 500 {
		m.serverErrors.Add(1)
	} else if status >= 400 {
		m.clientErrors.Add(1)
	}
	if webhook {
		if status >= 200 && status < 300 {
			m.webhookAccepted.Add(1)
		} else {
			m.webhookRejected.Add(1)
		}
	}
}

func (m *Metrics) ObserveWebhookDuplicate() { m.webhookDuplicates.Add(1) }

func (m *Metrics) WritePrometheus(w io.Writer, jobs repository.JobStats) error {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	_, err := fmt.Fprintf(w, `# HELP pr_tombstone_http_requests_total HTTP requests handled.
# TYPE pr_tombstone_http_requests_total counter
pr_tombstone_http_requests_total %d
# HELP pr_tombstone_http_client_errors_total HTTP 4xx responses.
# TYPE pr_tombstone_http_client_errors_total counter
pr_tombstone_http_client_errors_total %d
# HELP pr_tombstone_http_server_errors_total HTTP 5xx responses.
# TYPE pr_tombstone_http_server_errors_total counter
pr_tombstone_http_server_errors_total %d
# HELP pr_tombstone_webhooks_total GitHub webhook outcomes.
# TYPE pr_tombstone_webhooks_total counter
pr_tombstone_webhooks_total{outcome="accepted"} %d
pr_tombstone_webhooks_total{outcome="rejected"} %d
pr_tombstone_webhooks_total{outcome="duplicate"} %d
# HELP pr_tombstone_analysis_jobs Current jobs grouped by status.
# TYPE pr_tombstone_analysis_jobs gauge
pr_tombstone_analysis_jobs{status="pending"} %d
pr_tombstone_analysis_jobs{status="running"} %d
pr_tombstone_analysis_jobs{status="completed"} %d
pr_tombstone_analysis_jobs{status="failed"} %d
# HELP pr_tombstone_process_uptime_seconds Process uptime.
# TYPE pr_tombstone_process_uptime_seconds gauge
pr_tombstone_process_uptime_seconds %.0f
# HELP pr_tombstone_process_alloc_bytes Go heap bytes currently allocated.
# TYPE pr_tombstone_process_alloc_bytes gauge
pr_tombstone_process_alloc_bytes %d
# HELP pr_tombstone_build_info Build and release information.
# TYPE pr_tombstone_build_info gauge
pr_tombstone_build_info{version="%s"} 1
`, m.requests.Load(), m.clientErrors.Load(), m.serverErrors.Load(), m.webhookAccepted.Load(), m.webhookRejected.Load(), m.webhookDuplicates.Load(), jobs.Pending, jobs.Running, jobs.Completed, jobs.Failed, time.Since(m.started).Seconds(), memory.Alloc, version.Version)
	return err
}
