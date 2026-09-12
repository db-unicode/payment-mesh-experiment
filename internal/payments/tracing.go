package payments

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// NewTracerProvider creates a real OpenTelemetry provider. The standard
// OTLP/HTTP exporter is enabled when OTEL_EXPORTER_OTLP_ENDPOINT is set; an
// SDK provider without an exporter is used otherwise so local mocks remain
// useful without an observability stack.
func NewTracerProvider(ctx context.Context, serviceName string) (*sdktrace.TracerProvider, func(context.Context) error, error) {
	res, err := resource.New(ctx, resource.WithAttributes(attribute.String("service.name", serviceName)))
	if err != nil {
		return nil, nil, err
	}
	options := []sdktrace.TracerProviderOption{sdktrace.WithResource(res)}
	endpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"))
	if endpoint == "" {
		endpoint = strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	}
	if endpoint != "" {
		exporter, err := newOTLPHTTPExporter(ctx, endpoint)
		if err != nil {
			return nil, nil, err
		}
		options = append(options, sdktrace.WithBatcher(exporter))
	}
	provider := sdktrace.NewTracerProvider(options...)
	shutdown := func(shutdownCtx context.Context) error { return provider.Shutdown(shutdownCtx) }
	return provider, shutdown, nil
}

func newOTLPHTTPExporter(ctx context.Context, endpoint string) (sdktrace.SpanExporter, error) {
	options := make([]otlptracehttp.Option, 0, 3)
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid OTLP endpoint: %w", err)
	}
	if parsed.Scheme == "" {
		options = append(options, otlptracehttp.WithEndpoint(endpoint))
		options = append(options, otlptracehttp.WithURLPath("/v1/traces"))
	} else {
		if parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return nil, fmt.Errorf("invalid OTLP endpoint scheme")
		}
		options = append(options, otlptracehttp.WithEndpoint(parsed.Host))
		path := parsed.Path
		if path == "" || path == "/" {
			path = "/v1/traces"
		}
		options = append(options, otlptracehttp.WithURLPath(path))
		if parsed.Scheme == "http" {
			options = append(options, otlptracehttp.WithInsecure())
		}
	}
	return otlptracehttp.New(ctx, options...)
}

// TraceHTTPHandler extracts W3C trace context and starts a server span. It is
// intentionally transport-agnostic so external gateway calls can be observed
// by the same standard collector as mesh spans.
func TraceHTTPHandler(next http.Handler, tracer trace.Tracer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		ctx, span := tracer.Start(ctx, "HTTP "+r.Method+" "+boundedRoute(r.URL.Path), trace.WithSpanKind(trace.SpanKindServer))
		writer := &traceResponseWriter{ResponseWriter: w}
		next.ServeHTTP(writer, r.WithContext(ctx))
		status := writer.status
		if status == 0 {
			status = http.StatusOK
		}
		if requestErr := r.Context().Err(); requestErr != nil {
			if writer.status == 0 {
				status = http.StatusRequestTimeout
			}
			span.RecordError(requestErr)
			span.SetStatus(codes.Error, requestErr.Error())
		}
		span.SetAttributes(attribute.String("http.request.method", r.Method), attribute.Int("http.response.status_code", status))
		if status >= http.StatusInternalServerError {
			span.SetStatus(codes.Error, http.StatusText(status))
		}
		span.End()
	})
}

type traceResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *traceResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *traceResponseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func boundedRoute(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) >= 2 && parts[0] == "v1" && (parts[1] == "payments" || parts[1] == "participants") {
		parts = parts[:2]
	} else if len(parts) > 3 {
		parts = parts[:3]
	}
	if len(parts) == 0 || parts[0] == "" {
		return "/"
	}
	return "/" + strings.Join(parts, "/")
}
