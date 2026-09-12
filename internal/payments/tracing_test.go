package payments

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	collectortracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

func TestOTLPHTTPExporterSendsW3CChildSpan(t *testing.T) {
	type exportResult struct {
		request *collectortracepb.ExportTraceServiceRequest
		err     error
	}
	requests := make(chan exportResult, 1)
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/traces" {
			requests <- exportResult{err: fmt.Errorf("path = %q", r.URL.Path)}
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			requests <- exportResult{err: err}
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var exported collectortracepb.ExportTraceServiceRequest
		if err := proto.Unmarshal(body, &exported); err != nil {
			requests <- exportResult{err: err}
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requests <- exportResult{request: &exported}
		w.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", collector.URL)
	provider, shutdown, err := NewTracerProvider(context.Background(), "gateway-card")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	previous := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	defer otel.SetTextMapPropagator(previous)
	tracer := provider.Tracer("gateway-card")
	parentCtx, parent := tracer.Start(context.Background(), "caller")
	parentSpanID := parent.SpanContext().SpanID()
	parentTraceID := parent.SpanContext().TraceID()
	carrier := propagation.HeaderCarrier(http.Header{})
	otel.GetTextMapPropagator().Inject(parentCtx, carrier)
	request := httptest.NewRequest(http.MethodPost, "/v1/card/authorizations/123", nil)
	for key, values := range carrier {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	TraceHTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), tracer).ServeHTTP(httptest.NewRecorder(), request)
	parent.End()
	require.NoError(t, shutdown(ctx))
	select {
	case result := <-requests:
		require.NoError(t, result.err)
		exported := result.request
		require.Len(t, exported.ResourceSpans, 1)
		require.Len(t, exported.ResourceSpans[0].ScopeSpans, 1)
		spans := exported.ResourceSpans[0].ScopeSpans[0].Spans
		require.Len(t, spans, 2)
		var serverSpanParentMatches bool
		for _, span := range spans {
			if span.Name == "HTTP POST /v1/card/authorizations" {
				serverSpanParentMatches = bytes.Equal(span.ParentSpanId, parentSpanID[:]) && bytes.Equal(span.TraceId, parentTraceID[:])
			}
		}
		require.True(t, serverSpanParentMatches)
	case <-ctx.Done():
		t.Fatal("timed out waiting for OTLP export")
	}
}
