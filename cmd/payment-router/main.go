package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/academic/payment-mesh-experiment/internal/payments"
)

type server struct {
	db             *payments.DB
	circuits       map[string]*payments.Circuit
	metrics        payments.Metrics
	adapters       map[string]func(context.Context, payments.RouterPaymentRequest, string) payments.ProviderResult
	breakerEnabled bool
}

func main() {
	ctx := context.Background()
	db, err := openWithRetry(ctx, "ROUTER_DATABASE_URL")
	if err != nil {
		log.Fatal(err)
	}
	defer db.SQL.Close()
	if err = db.EnsurePayments(ctx); err != nil {
		log.Fatal(err)
	}
	insecure := getenv("GATEWAY_TLS_INSECURE", "false") == "true"
	providerClient := payments.NewProviderClient(2, insecure)
	card := payments.CardAdapter{BaseURL: getenv("GATEWAY_CARD_URL", "https://gateway-card:8443"), Client: providerClient}
	bank := payments.BankAdapter{BaseURL: getenv("GATEWAY_BANK_URL", "https://gateway-bank:8443"), Client: providerClient}
	s := &server{db: db, circuits: map[string]*payments.Circuit{"gateway-card": payments.NewCircuit(5, 30*time.Second), "gateway-bank": payments.NewCircuit(5, 30*time.Second)}, adapters: map[string]func(context.Context, payments.RouterPaymentRequest, string) payments.ProviderResult{"gateway-card": card.Charge, "gateway-bank": bank.Charge}, breakerEnabled: getenv("APP_CIRCUIT_BREAKER_ENABLED", "true") != "false"}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.health)
	mux.HandleFunc("/readyz", s.ready)
	mux.HandleFunc("/metrics", s.metrics.Handler)
	mux.HandleFunc("/internal/v1/payments", s.create)
	mux.HandleFunc("/v1/payments/", s.get)
	addr := getenv("HTTP_ADDR", ":8080")
	log.Printf("payment-router listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
func openWithRetry(ctx context.Context, n string) (*payments.DB, error) {
	var last error
	for i := 0; i < 30; i++ {
		db, e := payments.Open(ctx, n)
		if e == nil {
			return db, nil
		}
		last = e
		time.Sleep(time.Second)
	}
	return nil, last
}
func (s *server) health(w http.ResponseWriter, _ *http.Request) {
	payments.JSON(w, 200, map[string]string{"status": "ok"})
}
func (s *server) ready(w http.ResponseWriter, _ *http.Request) {
	if e := s.db.SQL.Ping(); e != nil {
		payments.JSON(w, 503, map[string]string{"status": "not_ready"})
		return
	}
	payments.JSON(w, 200, map[string]string{"status": "ready"})
}
func (s *server) create(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	if r.Method != http.MethodPost {
		payments.JSON(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 255 {
		payments.JSON(w, 400, map[string]string{"error": "Idempotency-Key is required"})
		return
	}
	var req payments.RouterPaymentRequest
	if e := payments.Decode(r, &req); e != nil {
		payments.JSON(w, 400, map[string]string{"error": "invalid JSON"})
		return
	}
	if e := payments.ValidateRouterRequest(req); e != nil {
		payments.JSON(w, 400, map[string]string{"error": e.Error()})
		return
	}
	hash, _ := payments.RequestHash(req)
	tx, e := s.db.SQL.BeginTx(r.Context(), nil)
	if e != nil {
		payments.JSON(w, 500, map[string]string{"error": "database unavailable"})
		return
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(r.Context(), `SELECT pg_advisory_xact_lock(hashtext($1))`, key); e != nil {
		payments.JSON(w, 500, map[string]string{"error": "database lock unavailable"})
		return
	}
	var existing payments.PaymentResponse
	var existingHash string
	var created time.Time
	e = tx.QueryRowContext(r.Context(), `SELECT id::text,idempotency_key,request_hash,status,gateway,COALESCE(provider_ref,''),COALESCE(error,''),created_at FROM payments WHERE idempotency_key=$1`, key).Scan(&existing.ID, &existing.IdempotencyKey, &existingHash, &existing.Status, &existing.Gateway, &existing.ProviderRef, &existing.Error, &created)
	if e == nil {
		if existingHash != hash {
			payments.JSON(w, 409, map[string]string{"error": "idempotency key already used with another payload"})
			return
		}
		existing.CreatedAt = created.UTC().Format(time.RFC3339Nano)
		payments.JSON(w, payments.StatusCode(existing.Status), existing)
		return
	}
	if !errors.Is(e, sql.ErrNoRows) {
		payments.JSON(w, 500, map[string]string{"error": "database read failed"})
		return
	}
	id := newUUID()
	gateway := gatewayFor(req.Instrument)
	if _, e = tx.ExecContext(r.Context(), `INSERT INTO payments(id,idempotency_key,request_hash,amount_minor,currency,instrument,participant_id,debtor_participant_id,creditor_participant_id,reference,status,gateway) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'PENDING',$11)`, id, key, hash, req.AmountMinor, strings.ToUpper(req.Currency), req.Instrument, req.DebtorParticipantID, req.DebtorParticipantID, req.CreditorParticipantID, req.Reference, gateway); e != nil {
		payments.JSON(w, 500, map[string]string{"error": "database insert failed"})
		return
	}
	if e = tx.Commit(); e != nil {
		payments.JSON(w, 500, map[string]string{"error": "database commit failed"})
		return
	}
	result := s.charge(r, req, key, gateway)
	_, _ = s.db.SQL.ExecContext(r.Context(), `UPDATE payments SET status=$1,provider_ref=$2,error=$3,updated_at=now() WHERE id=$4`, result.Status, result.ProviderRef, result.Error, id)
	result.ID = id
	result.IdempotencyKey = key
	result.Gateway = gateway
	result.CreatedAt = payments.Now()
	s.metrics.Count(result.Status == "SUCCEEDED", time.Since(started).Milliseconds())
	payments.JSON(w, payments.StatusCode(result.Status), result)
}
func (s *server) charge(r *http.Request, req payments.RouterPaymentRequest, key, gateway string) payments.PaymentResponse {
	c := s.circuits[gateway]
	if s.breakerEnabled && (c == nil || !c.Allow()) {
		return payments.PaymentResponse{Status: "PENDING", Error: "gateway circuit open"}
	}
	var lastErr error
	providerContext := payments.ContextWithTraceHeaders(r.Context(), payments.TraceHeaders(r))
	for attempt := 0; attempt < 2; attempt++ {
		result := s.adapters[gateway](providerContext, req, key)
		if result.Status == "PENDING" {
			lastErr = fmt.Errorf("%s", result.Error)
			continue
		}
		if s.breakerEnabled {
			c.Success()
		}
		return payments.PaymentResponse{Status: result.Status, ProviderRef: result.ProviderRef, Error: result.Error}
	}
	if s.breakerEnabled {
		c.Failure()
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("provider timeout")
	}
	return payments.PaymentResponse{Status: "PENDING", Error: lastErr.Error()}
}
func (s *server) get(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		payments.JSON(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/payments/")
	var out payments.PaymentResponse
	var created time.Time
	e := s.db.SQL.QueryRowContext(r.Context(), `SELECT id::text,idempotency_key,status,gateway,COALESCE(provider_ref,''),COALESCE(error,''),created_at FROM payments WHERE id=$1`, id).Scan(&out.ID, &out.IdempotencyKey, &out.Status, &out.Gateway, &out.ProviderRef, &out.Error, &created)
	if e != nil {
		payments.JSON(w, 404, map[string]string{"error": "payment not found"})
		return
	}
	out.CreatedAt = created.UTC().Format(time.RFC3339Nano)
	payments.JSON(w, payments.StatusCode(out.Status), out)
}
func gatewayFor(instrument string) string {
	switch strings.ToLower(instrument) {
	case "bank_transfer", "bank", "pse":
		return "gateway-bank"
	default:
		return "gateway-card"
	}
}
func newUUID() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return fmt.Sprintf("%s-%s-%s-%s-%s", h[:8], h[8:12], h[12:16], h[16:20], h[20:])
}
func getenv(k, f string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return f
}
