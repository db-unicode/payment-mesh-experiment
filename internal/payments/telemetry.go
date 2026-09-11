package payments

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
)

// Metrics is intentionally dependency-free: it exposes Prometheus text format and
// propagates W3C trace context while the Collector remains a deployable concern.
type Metrics struct {
	mu        sync.Mutex
	requests  uint64
	failures  uint64
	latencyMs uint64
	buckets   [6]uint64
}

func (m *Metrics) Count(ok bool, latencyMs int64) {
	atomic.AddUint64(&m.requests, 1)
	if !ok {
		atomic.AddUint64(&m.failures, 1)
	}
	atomic.AddUint64(&m.latencyMs, uint64(latencyMs))
	m.mu.Lock()
	for i, bound := range [...]int64{50, 100, 250, 500, 1000, 5000} {
		if latencyMs <= bound {
			for j := i; j < len(m.buckets); j++ {
				m.buckets[j]++
			}
			break
		}
	}
	m.mu.Unlock()
}

func (m *Metrics) Handler(w http.ResponseWriter, _ *http.Request) {
	r := atomic.LoadUint64(&m.requests)
	f := atomic.LoadUint64(&m.failures)
	l := atomic.LoadUint64(&m.latencyMs)
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "payments_requests_total %d\npayments_failures_total %d\npayments_latency_ms_sum %d\n", r, f, l)
	m.mu.Lock()
	buckets := m.buckets
	m.mu.Unlock()
	for i, bound := range [...]int{50, 100, 250, 500, 1000, 5000} {
		fmt.Fprintf(w, "payments_latency_ms_bucket{le=\"%d\"} %d\n", bound, buckets[i])
	}
	fmt.Fprintf(w, "payments_latency_ms_bucket{le=\"+Inf\"} %d\n", r)
	fmt.Fprintf(w, "payments_latency_ms_count %d\n", r)
}

type traceHeadersContextKey struct{}

func ContextWithTraceHeaders(ctx context.Context, headers http.Header) context.Context {
	return context.WithValue(ctx, traceHeadersContextKey{}, headers.Clone())
}

func TraceHeadersFromContext(ctx context.Context) http.Header {
	if headers, ok := ctx.Value(traceHeadersContextKey{}).(http.Header); ok {
		return headers.Clone()
	}
	return make(http.Header)
}

func TraceHeaders(req *http.Request) http.Header {
	h := make(http.Header)
	if v := req.Header.Get("traceparent"); v != "" {
		h.Set("traceparent", v)
	}
	if v := req.Header.Get("tracestate"); v != "" {
		h.Set("tracestate", v)
	}
	if v := req.Header.Get("x-request-id"); v != "" {
		h.Set("x-request-id", v)
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
