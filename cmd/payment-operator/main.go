package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/academic/payment-mesh-experiment/internal/payments"
)

type server struct {
	db      *payments.DB
	client  *http.Client
	metrics payments.Metrics
}

func main() {
	ctx := context.Background()
	db, e := openWithRetry(ctx, "OPERATOR_DATABASE_URL")
	if e != nil {
		log.Fatal(e)
	}
	defer db.SQL.Close()
	if e = db.EnsureOperator(ctx); e != nil {
		log.Fatal(e)
	}
	// The operator budget must exceed the router's two bounded provider attempts
	// so uncertain provider failures can be normalized to PENDING instead of 503.
	s := &server{db: db, client: &http.Client{Timeout: 6 * time.Second}}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.health)
	mux.HandleFunc("/readyz", s.ready)
	mux.HandleFunc("/metrics", s.metrics.Handler)
	mux.HandleFunc("/v1/payments", s.create)
	mux.HandleFunc("/v1/payments/", s.get)
	addr := getenv("HTTP_ADDR", ":8080")
	log.Printf("payment-operator listening on %s", addr)
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
	if key == "" {
		payments.JSON(w, 400, map[string]string{"error": "Idempotency-Key is required"})
		return
	}
	var req payments.PublicPaymentRequest
	if e := payments.Decode(r, &req); e != nil {
		payments.JSON(w, 400, map[string]string{"error": "invalid JSON"})
		return
	}
	if e := payments.ValidateRequest(req); e != nil {
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
	var out payments.PaymentResponse
	var oldHash string
	var created time.Time
	e = tx.QueryRowContext(r.Context(), `SELECT id::text,idempotency_key,request_hash,status,COALESCE(gateway,''),COALESCE(provider_ref,''),COALESCE(error,''),created_at FROM orders WHERE idempotency_key=$1`, key).Scan(&out.ID, &out.IdempotencyKey, &oldHash, &out.Status, &out.Gateway, &out.ProviderRef, &out.Error, &created)
	if e == nil {
		if oldHash != hash {
			payments.JSON(w, 409, map[string]string{"error": "idempotency key already used with another payload"})
			return
		}
		out.CreatedAt = created.UTC().Format(time.RFC3339Nano)
		payments.JSON(w, payments.StatusCode(out.Status), out)
		return
	}
	if !errors.Is(e, sql.ErrNoRows) {
		payments.JSON(w, 500, map[string]string{"error": "database read failed"})
		return
	}
	traceContext := payments.ContextWithTraceHeaders(r.Context(), payments.TraceHeaders(r))
	debtor, e := s.participant(traceContext, req.DebtorParticipantID)
	if e != nil {
		payments.JSON(w, 503, map[string]string{"error": e.Error()})
		return
	}
	creditor, e := s.participant(traceContext, req.CreditorParticipantID)
	if e != nil {
		payments.JSON(w, 503, map[string]string{"error": e.Error()})
		return
	}
	if !debtor.Active || !creditor.Active {
		payments.JSON(w, 409, map[string]string{"error": "participant inactive"})
		return
	}
	internal := payments.RouterPaymentRequest{DebtorParticipantID: req.DebtorParticipantID, CreditorParticipantID: req.CreditorParticipantID, AmountMinor: req.AmountMinor, Currency: req.Currency, Reference: req.Reference, Instrument: debtor.Instrument}
	routerURL := getenv("ROUTER_URL", "http://payment-router:8080") + "/internal/v1/payments"
	payload, _ := json.Marshal(internal)
	rr, e := http.NewRequestWithContext(r.Context(), http.MethodPost, routerURL, bytes.NewReader(payload))
	if e != nil {
		payments.JSON(w, 500, map[string]string{"error": "could not build router request"})
		return
	}
	rr.Header.Set("Content-Type", "application/json")
	rr.Header.Set("Idempotency-Key", key)
	for k, v := range payments.TraceHeaders(r) {
		rr.Header[k] = v
	}
	resp, e := s.client.Do(rr)
	if e != nil {
		payments.JSON(w, 503, map[string]string{"error": "router unavailable"})
		return
	}
	defer resp.Body.Close()
	if e = json.NewDecoder(resp.Body).Decode(&out); e != nil {
		payments.JSON(w, 502, map[string]string{"error": "invalid router response"})
		return
	}
	out.IdempotencyKey = key
	e = tx.QueryRowContext(r.Context(), `INSERT INTO orders(id,idempotency_key,request_hash,payment_id,status,gateway,provider_ref,error) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING created_at`, out.ID, key, hash, out.ID, out.Status, out.Gateway, out.ProviderRef, out.Error).Scan(&created)
	if e != nil {
		payments.JSON(w, 500, map[string]string{"error": "could not persist order"})
		return
	}
	out.CreatedAt = created.UTC().Format(time.RFC3339Nano)
	if e = tx.Commit(); e != nil {
		payments.JSON(w, 500, map[string]string{"error": "database commit failed"})
		return
	}
	s.metrics.Count(out.Status == "SUCCEEDED", time.Since(started).Milliseconds())
	payments.JSON(w, resp.StatusCode, out)
}

func (s *server) participant(ctx context.Context, id string) (payments.Participant, error) {
	var p payments.Participant
	url := getenv("PARTICIPANT_MANAGER_URL", "http://participant-payment-manager:8080") + "/v1/participants/" + id
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return p, err
	}
	for name, values := range payments.TraceHeadersFromContext(ctx) {
		req.Header[name] = values
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return p, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return p, errors.New(strings.TrimSpace(string(b)))
	}
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return p, errors.New("invalid participant response")
	}
	return p, nil
}
func (s *server) get(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		payments.JSON(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/payments/")
	var paymentID string
	var status string
	e := s.db.SQL.QueryRowContext(r.Context(), `SELECT payment_id::text,status FROM orders WHERE id=$1`, id).Scan(&paymentID, &status)
	if e != nil {
		payments.JSON(w, 404, map[string]string{"error": "payment not found"})
		return
	}
	routerURL := getenv("ROUTER_URL", "http://payment-router:8080") + "/v1/payments/" + paymentID
	resp, e := s.client.Get(routerURL)
	if e != nil {
		payments.JSON(w, 503, map[string]string{"error": "router unavailable"})
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
func getenv(k, f string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return f
}
