package payments

import (
	"fmt"
	"net/http"
	"strconv"
	"sync/atomic"
)

// Metrics is intentionally dependency-free: it exposes Prometheus text format and
// propagates W3C trace context while the Collector remains a deployable concern.
type Metrics struct {
	requests  uint64
	failures  uint64
	latencyMs uint64
}

func (m *Metrics) Count(ok bool, latencyMs int64) {
	atomic.AddUint64(&m.requests, 1)
	if !ok {
		atomic.AddUint64(&m.failures, 1)
	}
	atomic.AddUint64(&m.latencyMs, uint64(latencyMs))
}

func (m *Metrics) Handler(w http.ResponseWriter, _ *http.Request) {
	r := atomic.LoadUint64(&m.requests)
	f := atomic.LoadUint64(&m.failures)
	l := atomic.LoadUint64(&m.latencyMs)
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "payments_requests_total %d\npayments_failures_total %d\npayments_latency_ms_sum %d\n", r, f, l)
}

func TraceHeaders(req *http.Request) http.Header {
	h := make(http.Header)
	if v := req.Header.Get("traceparent"); v != "" {
		h.Set("traceparent", v)
	}
	if v := req.Header.Get("tracestate"); v != "" {
		h.Set("tracestate", v)
	}
	return h
}

func StatusCode(status string) int {
	switch status {
	case "SUCCEEDED":
		return http.StatusOK
	case "FAILED":
		return http.StatusUnprocessableEntity
	default:
		return http.StatusAccepted
	}
}

func Int(v int64) string { return strconv.FormatInt(v, 10) }
