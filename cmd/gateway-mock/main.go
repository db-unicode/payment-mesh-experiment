package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/academic/payment-mesh-experiment/internal/payments"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

type cardRequest struct {
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
	Reference   string `json:"reference"`
	RequestID   string `json:"request_id"`
}
type bankRequest struct {
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
	Reference   string `json:"reference"`
	TransferID  string `json:"transfer_id"`
}
type cardResult struct {
	State           string `json:"state"`
	AuthorizationID string `json:"authorization_id,omitempty"`
	Message         string `json:"message,omitempty"`
}
type bankResult struct {
	Decision   string `json:"decision"`
	TransferID string `json:"transfer_id,omitempty"`
	Message    string `json:"message,omitempty"`
}
type stats struct {
	Attempts         int `json:"attempts"`
	EffectiveCharges int `json:"effective_charges"`
}

var state = struct {
	sync.Mutex
	card  map[string]cardResult
	bank  map[string]bankResult
	stats stats
}{card: map[string]cardResult{}, bank: map[string]bankResult{}}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx := context.Background()
	serviceName := getenv("OTEL_SERVICE_NAME", getenv("GATEWAY_NAME", "gateway"))
	tracerProvider, shutdownTracer, err := payments.NewTracerProvider(ctx, serviceName)
	if err != nil {
		return err
	}
	otel.SetTracerProvider(tracerProvider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if shutdownErr := shutdownTracer(shutdownCtx); shutdownErr != nil {
			log.Printf("gateway tracer shutdown: %v", shutdownErr)
		}
	}()
	if err := loadState(); err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", health)
	// Control endpoints are separate from the payment contract. They are
	// intended for the local experiment runner and require their own token.
	mux.Handle("/admin/behavior", controlAuth(http.HandlerFunc(behavior)))
	mux.Handle("/stats", controlAuth(http.HandlerFunc(statsHandler)))
	// Only the payment contract is protected. The behavior and stats endpoints
	// remain available to the local experiment runner and never accept payment
	// requests, keeping operational control separate from gateway traffic.
	mux.Handle("/v1/card/authorizations", paymentAuth(http.HandlerFunc(cardAuthorization)))
	mux.Handle("/v2/transfers", paymentAuth(http.HandlerFunc(bankTransfer)))
	addr := getenv("HTTP_ADDR", ":8080")
	handler := payments.TraceHTTPHandler(mux, tracerProvider.Tracer(serviceName))
	server := &http.Server{Addr: addr, Handler: handler}
	serveErr := make(chan error, 1)
	go func() {
		if cert, key := os.Getenv("TLS_CERT_FILE"), os.Getenv("TLS_KEY_FILE"); cert != "" && key != "" {
			serveErr <- server.ListenAndServeTLS(cert, key)
			return
		}
		serveErr <- server.ListenAndServe()
	}()
	log.Printf("gateway mock listening on %s (%s)", addr, serviceName)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(stop)
	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-stop:
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return nil
	}
}
func health(w http.ResponseWriter, _ *http.Request) { write(w, 200, map[string]string{"status": "ok"}) }
func cardAuthorization(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		write(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	var req cardRequest
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.RequestID == "" {
		write(w, 400, map[string]string{"error": "request_id is required"})
		return
	}
	state.Lock()
	state.stats.Attempts++
	if old, ok := state.card[req.RequestID]; ok {
		state.Unlock()
		write(w, 200, old)
		return
	}
	state.Unlock()
	if waitProvider(r) {
		return
	}
	behavior := getenv("GATEWAY_BEHAVIOR", "success")
	if behavior == "error" {
		write(w, 503, map[string]string{"message": "provider unavailable"})
		return
	}
	if behavior == "decline" {
		res := cardResult{State: "DECLINED", Message: "card authorization declined"}
		state.Lock()
		if old, ok := state.card[req.RequestID]; ok {
			state.Unlock()
			write(w, 200, old)
			return
		}
		state.card[req.RequestID] = res
		if err := persistStateLocked(); err != nil {
			delete(state.card, req.RequestID)
			state.Unlock()
			write(w, http.StatusServiceUnavailable, map[string]string{"message": "gateway state unavailable"})
			return
		}
		state.Unlock()
		write(w, 200, res)
		return
	}
	res := cardResult{State: "AUTHORIZED", AuthorizationID: fmt.Sprintf("card-auth-%d", time.Now().UnixNano())}
	state.Lock()
	if old, ok := state.card[req.RequestID]; ok {
		state.Unlock()
		write(w, 200, old)
		return
	}
	state.card[req.RequestID] = res
	state.stats.EffectiveCharges++
	if err := persistStateLocked(); err != nil {
		delete(state.card, req.RequestID)
		state.stats.EffectiveCharges--
		state.Unlock()
		write(w, http.StatusServiceUnavailable, map[string]string{"message": "gateway state unavailable"})
		return
	}
	state.Unlock()
	write(w, 200, res)
}
func bankTransfer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		write(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	var req bankRequest
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.TransferID == "" {
		write(w, 400, map[string]string{"error": "transfer_id is required"})
		return
	}
	state.Lock()
	state.stats.Attempts++
	if old, ok := state.bank[req.TransferID]; ok {
		state.Unlock()
		write(w, 200, old)
		return
	}
	state.Unlock()
	if waitProvider(r) {
		return
	}
	behavior := getenv("GATEWAY_BEHAVIOR", "success")
	if behavior == "error" {
		write(w, 503, map[string]string{"message": "bank provider unavailable"})
		return
	}
	if behavior == "decline" {
		res := bankResult{Decision: "REJECTED", Message: "bank transfer rejected"}
		state.Lock()
		if old, ok := state.bank[req.TransferID]; ok {
			state.Unlock()
			write(w, 200, old)
			return
		}
		state.bank[req.TransferID] = res
		if err := persistStateLocked(); err != nil {
			delete(state.bank, req.TransferID)
			state.Unlock()
			write(w, http.StatusServiceUnavailable, map[string]string{"message": "gateway state unavailable"})
			return
		}
		state.Unlock()
		write(w, 200, res)
		return
	}
	res := bankResult{Decision: "ACCEPTED", TransferID: fmt.Sprintf("bank-transfer-%d", time.Now().UnixNano())}
	state.Lock()
	if old, ok := state.bank[req.TransferID]; ok {
		state.Unlock()
		write(w, 200, old)
		return
	}
	state.bank[req.TransferID] = res
	state.stats.EffectiveCharges++
	if err := persistStateLocked(); err != nil {
		delete(state.bank, req.TransferID)
		state.stats.EffectiveCharges--
		state.Unlock()
		write(w, http.StatusServiceUnavailable, map[string]string{"message": "gateway state unavailable"})
		return
	}
	state.Unlock()
	write(w, 200, res)
}

type persistedState struct {
	Card  map[string]cardResult `json:"card"`
	Bank  map[string]bankResult `json:"bank"`
	Stats stats                 `json:"stats"`
}

func statePath() string { return strings.TrimSpace(os.Getenv("GATEWAY_STATE_FILE")) }

func loadState() error {
	path := statePath()
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var saved persistedState
	if err := json.Unmarshal(b, &saved); err != nil {
		return err
	}
	state.Lock()
	defer state.Unlock()
	if saved.Card != nil {
		state.card = saved.Card
	}
	if saved.Bank != nil {
		state.bank = saved.Bank
	}
	state.stats = saved.Stats
	return nil
}

// persistStateLocked writes idempotency results before a successful provider
// response is returned. Rename makes the file atomic across process crashes;
// callers must hold state's mutex.
func persistStateLocked() error {
	path := statePath()
	if path == "" {
		return nil
	}
	saved, err := json.Marshal(persistedState{Card: state.card, Bank: state.bank, Stats: state.stats})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, saved, 0o640); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func paymentAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expected := strings.TrimSpace(os.Getenv("GATEWAY_PAYMENT_TOKEN"))
		if expected == "" {
			expected = strings.TrimSpace(os.Getenv("GATEWAY_AUTH_TOKEN"))
		}
		provided := strings.TrimSpace(r.Header.Get("X-Gateway-Auth"))
		if provided == "" {
			provided = strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		}
		// A missing token is a configuration error, not an invitation to expose
		// the external payment endpoint without authentication.
		if expected == "" {
			write(w, http.StatusServiceUnavailable, map[string]string{"error": "gateway authentication is not configured"})
			return
		}
		if provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
			write(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func controlAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Control calls are authenticated even when the service is exposed on a
		// host-local port: a reverse proxy can make a remote caller appear local.
		expected := strings.TrimSpace(os.Getenv("GATEWAY_ADMIN_TOKEN"))
		provided := strings.TrimSpace(r.Header.Get("X-Gateway-Admin"))
		if provided == "" {
			provided = strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		}
		if expected == "" {
			write(w, http.StatusServiceUnavailable, map[string]string{"error": "gateway control authentication is not configured"})
			return
		}
		if provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
			write(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}
func waitProvider(r *http.Request) bool {
	delay, _ := strconv.Atoi(getenv("GATEWAY_DELAY_MS", "0"))
	if getenv("GATEWAY_BEHAVIOR", "success") == "timeout" {
		delay = 10000
	}
	if delay <= 0 {
		return false
	}
	timer := time.NewTimer(time.Duration(delay) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
		return false
	case <-r.Context().Done():
		return true
	}
}
func behavior(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		write(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	var b struct {
		Behavior string `json:"behavior"`
		DelayMs  int    `json:"delay_ms"`
	}
	if json.NewDecoder(r.Body).Decode(&b) != nil || b.Behavior == "" {
		write(w, 400, map[string]string{"error": "behavior is required"})
		return
	}
	os.Setenv("GATEWAY_BEHAVIOR", b.Behavior)
	os.Setenv("GATEWAY_DELAY_MS", strconv.Itoa(b.DelayMs))
	write(w, 200, b)
}
func statsHandler(w http.ResponseWriter, _ *http.Request) {
	state.Lock()
	defer state.Unlock()
	write(w, 200, state.stats)
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func getenv(k, f string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return f
}
