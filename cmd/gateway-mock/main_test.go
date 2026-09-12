package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/academic/payment-mesh-experiment/internal/payments"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func resetState() {
	state.Lock()
	defer state.Unlock()
	state.card = map[string]cardResult{}
	state.bank = map[string]bankResult{}
	state.stats = stats{}
}

func TestDeclinesAreAttemptsButNotEffectiveCharges(t *testing.T) {
	resetState()
	t.Setenv("GATEWAY_BEHAVIOR", "decline")
	t.Setenv("GATEWAY_DELAY_MS", "0")
	request := httptest.NewRequest(http.MethodPost, "/v1/card/authorizations", strings.NewReader(`{"request_id":"declined-1"}`))
	cardAuthorization(httptest.NewRecorder(), request)

	state.Lock()
	got := state.stats
	state.Unlock()
	if got.Attempts != 1 || got.EffectiveCharges != 0 {
		t.Fatalf("stats = %+v, want one attempt and zero effective charges", got)
	}
}

func TestPaymentAuthProtectsOnlyPaymentEndpoints(t *testing.T) {
	t.Setenv("GATEWAY_PAYMENT_TOKEN", "secret")
	without := httptest.NewRecorder()
	paymentAuth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })).ServeHTTP(without, httptest.NewRequest(http.MethodPost, "/v1/card/authorizations", nil))
	if without.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated payment status = %d, want 401", without.Code)
	}

	with := httptest.NewRequest(http.MethodPost, "/v1/card/authorizations", nil)
	with.Header.Set("X-Gateway-Auth", "secret")
	withResponse := httptest.NewRecorder()
	paymentAuth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })).ServeHTTP(withResponse, with)
	if withResponse.Code != http.StatusNoContent {
		t.Fatalf("authenticated payment status = %d, want 204", withResponse.Code)
	}

	// The stats handler remains a separate control handler from payment auth.
	statsResponse := httptest.NewRecorder()
	statsHandler(statsResponse, httptest.NewRequest(http.MethodGet, "/stats", nil))
	if statsResponse.Code != http.StatusOK {
		t.Fatalf("stats status = %d, want 200", statsResponse.Code)
	}
}

func TestControlAuthRequiresSeparateToken(t *testing.T) {
	t.Setenv("GATEWAY_ADMIN_TOKEN", "")
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	remote := httptest.NewRequest(http.MethodGet, "/stats", nil)
	remote.RemoteAddr = "10.0.0.4:5000"
	response := httptest.NewRecorder()
	controlAuth(next).ServeHTTP(response, remote)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("remote control without token status = %d, want 503", response.Code)
	}

	t.Setenv("GATEWAY_ADMIN_TOKEN", "admin-secret")
	local := httptest.NewRequest(http.MethodGet, "/stats", nil)
	local.RemoteAddr = "127.0.0.1:5000"
	local.Header.Set("X-Gateway-Admin", "admin-secret")
	response = httptest.NewRecorder()
	controlAuth(next).ServeHTTP(response, local)
	if response.Code != http.StatusNoContent {
		t.Fatalf("authenticated local control status = %d, want 204", response.Code)
	}

	remote.Header.Set("X-Gateway-Admin", "admin-secret")
	response = httptest.NewRecorder()
	controlAuth(next).ServeHTTP(response, remote)
	if response.Code != http.StatusNoContent {
		t.Fatalf("authenticated remote control status = %d, want 204", response.Code)
	}
}

func TestProviderKeysSurviveStateReload(t *testing.T) {
	resetState()
	stateDir := t.TempDir()
	t.Setenv("GATEWAY_STATE_FILE", filepath.Join(stateDir, "gateway.json"))
	t.Setenv("GATEWAY_BEHAVIOR", "success")
	request := httptest.NewRequest(http.MethodPost, "/v1/card/authorizations", strings.NewReader(`{"request_id":"durable-1"}`))
	response := httptest.NewRecorder()
	cardAuthorization(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("first response status = %d, want 200", response.Code)
	}

	resetState()
	if err := loadState(); err != nil {
		t.Fatalf("loadState() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "gateway.json")); err != nil {
		t.Fatalf("state file missing: %v", err)
	}
	duplicate := httptest.NewRecorder()
	cardAuthorization(duplicate, httptest.NewRequest(http.MethodPost, "/v1/card/authorizations", strings.NewReader(`{"request_id":"durable-1"}`)))
	if duplicate.Code != http.StatusOK {
		t.Fatalf("duplicate response status = %d, want 200", duplicate.Code)
	}
	state.Lock()
	charges := state.stats.EffectiveCharges
	state.Unlock()
	if charges != 1 {
		t.Fatalf("effective charges = %d, want 1 after reload", charges)
	}
}

func TestGatewayTraceSpanInheritsW3CParentAndRecordsStatus(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(exporter)))
	defer provider.Shutdown(context.Background())
	tracer := provider.Tracer("gateway-test")
	previous := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	defer otel.SetTextMapPropagator(previous)

	parentCtx, parent := tracer.Start(context.Background(), "caller")
	defer parent.End()
	carrier := propagation.HeaderCarrier(http.Header{})
	otel.GetTextMapPropagator().Inject(parentCtx, carrier)
	request := httptest.NewRequest(http.MethodPost, "/v1/card/authorizations/payment-123", nil)
	for key, values := range carrier {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	response := httptest.NewRecorder()
	payments.TraceHTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}), tracer).ServeHTTP(response, request)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("exported spans = %d, want server span only before ending parent", len(spans))
	}
	span := spans[0]
	if !span.SpanContext.IsValid() || span.Parent.SpanID() != parent.SpanContext().SpanID() {
		t.Fatalf("server span parent = %s, want %s", span.Parent.SpanID(), parent.SpanContext().SpanID())
	}
	if span.Name != "HTTP POST /v1/card/authorizations" {
		t.Fatalf("server span name = %q", span.Name)
	}
	if len(span.Attributes) == 0 {
		t.Fatal("server span missing HTTP attributes")
	}
	var sawMethod, sawStatus bool
	for _, attr := range span.Attributes {
		if string(attr.Key) == "http.request.method" && attr.Value.AsString() == "POST" {
			sawMethod = true
		}
		if string(attr.Key) == "http.response.status_code" && attr.Value.AsInt64() == http.StatusBadGateway {
			sawStatus = true
		}
	}
	if !sawMethod || !sawStatus {
		t.Fatalf("server span attributes = %#v", span.Attributes)
	}
	if span.Status.Code != codes.Error {
		t.Fatalf("server span status = %s, want Error", span.Status.Code)
	}
}
