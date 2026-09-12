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

type routerPaymentLookup struct {
	payments.PaymentResponse
	payments.RouterPaymentRequest
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
	// The operator budget must cover the mesh's bounded internal retry budget so
	// uncertain provider failures can be normalized to PENDING instead of 503.
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
	if key == "" || len(key) > 255 {
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
	hash, e := payments.RequestHash(req)
	if e != nil {
		payments.JSON(w, 400, map[string]string{"error": "could not hash request"})
		return
	}
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
		out.Status = payments.NormalizeStatus(out.Status)
		if out.Status == "PENDING" {
			// The operator's PENDING row is a cache of the router state. Read
			// through once on a replay so a completed charge is not kept stale.
			if refreshed, refreshErr := s.fetchRouter(r.Context(), out.ID, payments.TraceHeaders(r)); refreshErr == nil {
				if refreshed.Status == "PENDING" {
					// A GET only observes the router. Replaying its frozen internal
					// request lets the router acquire its recovery lease when the
					// PENDING row is stale, even if participant data has changed.
					if refreshed.Instrument != "" {
						if retried, retryErr := s.retryPending(r.Context(), refreshed.RouterPaymentRequest, key, payments.TraceHeaders(r)); retryErr == nil {
							refreshed = retried
						}
					}
				}
				refreshed.IdempotencyKey = key
				refreshed.Status = payments.NormalizeStatus(refreshed.Status)
				if refreshErr = s.updateOrderTx(r.Context(), tx, out.ID, refreshed.PaymentResponse); refreshErr != nil {
					payments.JSON(w, 503, map[string]string{"error": "order result persistence unavailable"})
					return
				}
				out = refreshed.PaymentResponse
			}
		}
		out.CreatedAt = created.UTC().Format(time.RFC3339Nano)
		if e = tx.Commit(); e != nil {
			payments.JSON(w, 500, map[string]string{"error": "database commit failed"})
			return
		}
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
	if !hasRole(debtor, payments.RoleDebtor) || !hasRole(creditor, payments.RoleCreditor) {
		payments.JSON(w, http.StatusForbidden, map[string]string{"error": "participant role does not permit this payment"})
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
	if e = json.NewDecoder(resp.Body).Decode(&out); e != nil || strings.TrimSpace(out.ID) == "" {
		payments.JSON(w, 502, map[string]string{"error": "invalid router response"})
		return
	}
	out.IdempotencyKey = key
	out.Status = payments.NormalizeStatus(out.Status)
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
	payments.JSON(w, payments.StatusCode(out.Status), out)
}

func (s *server) updateOrderTx(ctx context.Context, tx *sql.Tx, id string, out payments.PaymentResponse) error {
	result, err := tx.ExecContext(ctx, `UPDATE orders SET payment_id=$1,status=$2,gateway=$3,provider_ref=$4,error=$5 WHERE id=$6`, out.ID, payments.NormalizeStatus(out.Status), out.Gateway, out.ProviderRef, out.Error, id)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return errors.New("order was not updated")
	}
	return nil
}

func (s *server) fetchRouter(ctx context.Context, paymentID string, trace http.Header) (routerPaymentLookup, error) {
	var out routerPaymentLookup
	url := getenv("ROUTER_URL", "http://payment-router:8080") + "/v1/payments/" + paymentID
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return out, err
	}
	for key, values := range trace {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if err = json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return out, errors.New("invalid router response")
	}
	// 422 is the router's normal FAILED payment response and still carries a
	// valid domain result. Transport and lookup errors remain failures here.
	if resp.StatusCode >= http.StatusInternalServerError || resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusBadRequest {
		return out, errors.New("router lookup failed")
	}
	if strings.TrimSpace(out.ID) == "" {
		return out, errors.New("invalid router response")
	}
	out.Status = payments.NormalizeStatus(out.Status)
	return out, nil
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
		var upstreamError struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&upstreamError) == nil && upstreamError.Error != "" {
			return p, errors.New(upstreamError.Error)
		}
		return p, errors.New("participant lookup failed")
	}
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return p, errors.New("invalid participant response")
	}
	return p, nil
}

func hasRole(p payments.Participant, role string) bool {
	for _, candidate := range p.Roles {
		if strings.EqualFold(strings.TrimSpace(candidate), role) {
			return true
		}
	}
	return false
}

func (s *server) retryPending(ctx context.Context, internal payments.RouterPaymentRequest, key string, trace http.Header) (routerPaymentLookup, error) {
	payload, err := json.Marshal(internal)
	if err != nil {
		return routerPaymentLookup{}, err
	}
	routerURL := getenv("ROUTER_URL", "http://payment-router:8080") + "/internal/v1/payments"
	routerReq, err := http.NewRequestWithContext(ctx, http.MethodPost, routerURL, bytes.NewReader(payload))
	if err != nil {
		return routerPaymentLookup{}, err
	}
	routerReq.Header.Set("Content-Type", "application/json")
	routerReq.Header.Set("Idempotency-Key", key)
	for name, values := range trace {
		for _, value := range values {
			routerReq.Header.Add(name, value)
		}
	}
	resp, err := s.client.Do(routerReq)
	if err != nil {
		return routerPaymentLookup{}, err
	}
	defer resp.Body.Close()
	var out routerPaymentLookup
	if err = json.NewDecoder(resp.Body).Decode(&out); err != nil || strings.TrimSpace(out.ID) == "" {
		return routerPaymentLookup{}, errors.New("invalid router response")
	}
	if resp.StatusCode >= http.StatusBadRequest && resp.StatusCode != http.StatusUnprocessableEntity {
		return routerPaymentLookup{}, errors.New("router request failed")
	}
	out.RouterPaymentRequest = internal
	out.Status = payments.NormalizeStatus(out.Status)
	out.IdempotencyKey = key
	return out, nil
}

func (s *server) get(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		payments.JSON(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/payments/")
	var paymentID, key, status, gateway, providerRef, paymentError string
	var created time.Time
	e := s.db.SQL.QueryRowContext(r.Context(), `SELECT COALESCE(payment_id::text,''),idempotency_key,status,COALESCE(gateway,''),COALESCE(provider_ref,''),COALESCE(error,''),created_at FROM orders WHERE id=$1`, id).Scan(&paymentID, &key, &status, &gateway, &providerRef, &paymentError, &created)
	if e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			payments.JSON(w, 404, map[string]string{"error": "payment not found"})
		} else {
			payments.JSON(w, 500, map[string]string{"error": "database read failed"})
		}
		return
	}
	local := payments.PaymentResponse{ID: paymentID, IdempotencyKey: key, Status: payments.NormalizeStatus(status), Gateway: gateway, ProviderRef: providerRef, Error: paymentError, CreatedAt: created.UTC().Format(time.RFC3339Nano)}
	if paymentID == "" {
		payments.JSON(w, payments.StatusCode(local.Status), local)
		return
	}
	resp, e := s.fetchRouter(r.Context(), paymentID, payments.TraceHeaders(r))
	if e != nil {
		// A read-through failure must not turn a known PENDING operation into a
		// false terminal result. Return the last durable operator state.
		payments.JSON(w, payments.StatusCode(local.Status), local)
		return
	}
	resp.IdempotencyKey = key
	resp.Status = payments.NormalizeStatus(resp.Status)
	if resp.CreatedAt == "" {
		resp.CreatedAt = local.CreatedAt
	}
	if _, updateErr := s.db.SQL.ExecContext(r.Context(), `UPDATE orders SET status=$1,gateway=$2,provider_ref=$3,error=$4 WHERE id=$5`, resp.Status, resp.Gateway, resp.ProviderRef, resp.Error, id); updateErr != nil {
		// The router is authoritative for this read, but a later request will
		// retry the local reconciliation when the operator DB is available.
	}
	payments.JSON(w, payments.StatusCode(resp.Status), resp.PaymentResponse)
}
func getenv(k, f string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return f
}
