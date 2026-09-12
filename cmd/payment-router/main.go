package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
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

// paymentLookup includes the frozen internal request only on the router's
// internal lookup response. The operator uses it to recover an old PENDING
// operation without re-reading mutable participant data.
type paymentLookup struct {
	payments.PaymentResponse
	payments.RouterPaymentRequest
}

const defaultPendingRecoveryAfter = 30 * time.Second

// pendingRecoveryAfter bounds how long an operation may remain in the
// uncertain state before a retry is allowed. The provider idempotency key is
// reused for that retry, so a provider that already accepted the charge will
// return the original result instead of creating a second charge.
func pendingRecoveryAfter() time.Duration {
	value := strings.TrimSpace(os.Getenv("PENDING_RECOVERY_AFTER"))
	if value == "" {
		return defaultPendingRecoveryAfter
	}
	d, err := time.ParseDuration(value)
	if err != nil || d < 0 {
		return defaultPendingRecoveryAfter
	}
	return d
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
	authToken := strings.TrimSpace(getenv("GATEWAY_PAYMENT_TOKEN", getenv("GATEWAY_AUTH_TOKEN", "")))
	providerClient := payments.NewProviderClient(3, insecure)
	card := payments.CardAdapter{BaseURL: getenv("GATEWAY_CARD_URL", "https://gateway-card:8443"), Client: providerClient, AuthToken: authToken}
	bank := payments.BankAdapter{BaseURL: getenv("GATEWAY_BANK_URL", "https://gateway-bank:8443"), Client: providerClient, AuthToken: authToken}
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
	originalReq := req
	// Persist and hash one canonical internal representation. PostgreSQL's
	// CHAR(3) returns the stored uppercase currency, so this also makes a
	// recovery replay equivalent when the original request used lowercase.
	req.Currency = strings.ToUpper(strings.TrimSpace(req.Currency))
	req.Instrument = strings.ToLower(strings.TrimSpace(req.Instrument))
	if e := payments.ValidateRouterRequest(req); e != nil {
		payments.JSON(w, 400, map[string]string{"error": e.Error()})
		return
	}
	hash, e := payments.RequestHash(req)
	if e != nil {
		payments.JSON(w, 400, map[string]string{"error": "could not hash request"})
		return
	}
	legacyHash, _ := payments.RequestHash(originalReq)
	legacyCanonicalReq := req
	legacyCanonicalReq.Currency = strings.ToLower(req.Currency)
	legacyCanonicalHash, _ := payments.RequestHash(legacyCanonicalReq)
	// Hold a PostgreSQL session advisory lock for the whole reservation,
	// provider call, and result update. The reservation itself is committed
	// before the provider call so an uncertain result remains recoverable after
	// a process crash; the session lock serializes concurrent duplicates.
	dbCtx, cancelDB := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
	defer cancelDB()
	conn, e := s.db.SQL.Conn(dbCtx)
	if e != nil {
		payments.JSON(w, 500, map[string]string{"error": "database unavailable"})
		return
	}
	defer conn.Close()
	if _, e = conn.ExecContext(dbCtx, `SELECT pg_advisory_lock(hashtext($1))`, key); e != nil {
		payments.JSON(w, 500, map[string]string{"error": "database lock unavailable"})
		return
	}
	defer func() {
		unlockCtx, unlockCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer unlockCancel()
		var unlocked bool
		if err := conn.QueryRowContext(unlockCtx, `SELECT pg_advisory_unlock(hashtext($1))`, key).Scan(&unlocked); err != nil || !unlocked {
			// Returning a connection that may still hold a session lock would
			// block every future request using the same key. Mark it bad so the
			// pool discards the physical connection instead.
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	tx, e := conn.BeginTx(dbCtx, nil)
	if e != nil {
		payments.JSON(w, 500, map[string]string{"error": "database unavailable"})
		return
	}
	defer tx.Rollback()
	var existing payments.PaymentResponse
	var existingHash string
	var created, updated time.Time
	var lease time.Time
	shouldCharge := false
	e = tx.QueryRowContext(dbCtx, `SELECT id::text,idempotency_key,request_hash,status,gateway,COALESCE(provider_ref,''),COALESCE(error,''),created_at,updated_at FROM payments WHERE idempotency_key=$1`, key).Scan(&existing.ID, &existing.IdempotencyKey, &existingHash, &existing.Status, &existing.Gateway, &existing.ProviderRef, &existing.Error, &created, &updated)
	found := e == nil
	if e == nil {
		if existingHash != hash && existingHash != legacyHash && existingHash != legacyCanonicalHash {
			payments.JSON(w, 409, map[string]string{"error": "idempotency key already used with another payload"})
			return
		}
		existing.Status = payments.NormalizeStatus(existing.Status)
		existing.CreatedAt = created.UTC().Format(time.RFC3339Nano)
		if existing.Status == "PENDING" && time.Since(updated) >= pendingRecoveryAfter() {
			// Claim recovery while the per-key advisory lock is held. A second
			// request will observe the refreshed updated_at and return PENDING.
			if e = tx.QueryRowContext(dbCtx, `UPDATE payments SET error=$1,updated_at=now() WHERE id=$2 AND status='PENDING' RETURNING updated_at`, "recovering payment", existing.ID).Scan(&lease); e != nil {
				payments.JSON(w, 500, map[string]string{"error": "could not claim pending payment"})
				return
			}
			shouldCharge = true
		} else {
			if e = tx.Commit(); e != nil {
				payments.JSON(w, 500, map[string]string{"error": "database commit failed"})
				return
			}
			payments.JSON(w, payments.StatusCode(existing.Status), existing)
			return
		}
	}
	if !found && !errors.Is(e, sql.ErrNoRows) {
		payments.JSON(w, 500, map[string]string{"error": "database read failed"})
		return
	}
	id := newUUID()
	gateway := gatewayFor(req.Instrument)
	if !shouldCharge {
		if e = tx.QueryRowContext(dbCtx, `INSERT INTO payments(id,idempotency_key,request_hash,amount_minor,currency,instrument,participant_id,debtor_participant_id,creditor_participant_id,reference,status,gateway) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'PENDING',$11) RETURNING created_at,updated_at`, id, key, hash, req.AmountMinor, strings.ToUpper(req.Currency), req.Instrument, req.DebtorParticipantID, req.DebtorParticipantID, req.CreditorParticipantID, req.Reference, gateway).Scan(&created, &lease); e != nil {
			payments.JSON(w, 500, map[string]string{"error": "database insert failed"})
			return
		}
		shouldCharge = true
	} else {
		id = existing.ID
		gateway = existing.Gateway
	}
	if e = tx.Commit(); e != nil {
		payments.JSON(w, 500, map[string]string{"error": "database commit failed"})
		return
	}
	result := payments.PaymentResponse{Status: "PENDING", Error: "payment processing in progress"}
	if shouldCharge {
		result = s.charge(r, req, key, gateway)
		result.Status = payments.NormalizeStatus(result.Status)
		persistCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
		e = s.persistResultConn(persistCtx, conn, id, result, lease)
		cancel()
		if e != nil {
			// Never report a terminal result that was not durably recorded. The
			// committed PENDING reservation remains available for a later retry;
			// providers must deduplicate the reused key.
			result.Status = "PENDING"
			result.Error = "payment result persistence unavailable"
		}
	}
	result.ID = id
	result.IdempotencyKey = key
	result.Gateway = gateway
	result.CreatedAt = created.UTC().Format(time.RFC3339Nano)
	s.metrics.Count(result.Status == "SUCCEEDED", time.Since(started).Milliseconds())
	payments.JSON(w, payments.StatusCode(result.Status), result)
}

func (s *server) persistResultConn(ctx context.Context, conn *sql.Conn, id string, result payments.PaymentResponse, lease time.Time) error {
	result.Status = payments.NormalizeStatus(result.Status)
	res, err := conn.ExecContext(ctx, `UPDATE payments SET status=$1,provider_ref=$2,error=$3,updated_at=now() WHERE id=$4 AND status='PENDING' AND updated_at=$5`, result.Status, result.ProviderRef, result.Error, id, lease)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("payment %s was not updated", id)
	}
	return nil
}
func (s *server) charge(r *http.Request, req payments.RouterPaymentRequest, key, gateway string) payments.PaymentResponse {
	c := s.circuits[gateway]
	if s.breakerEnabled && (c == nil || !c.Allow()) {
		return payments.PaymentResponse{Status: "PENDING", Error: "gateway circuit open"}
	}
	providerContext := payments.ContextWithTraceHeaders(r.Context(), payments.TraceHeaders(r))
	result := s.adapters[gateway](providerContext, req, key)
	result.Status = payments.NormalizeStatus(result.Status)
	if result.Status == "PENDING" {
		if s.breakerEnabled {
			c.Failure()
		}
		if result.Error == "" {
			result.Error = "provider response uncertain"
		}
		return payments.PaymentResponse{Status: "PENDING", ProviderRef: result.ProviderRef, Error: result.Error}
	}
	if s.breakerEnabled {
		c.Success()
	}
	return payments.PaymentResponse{Status: result.Status, ProviderRef: result.ProviderRef, Error: result.Error}
}
func (s *server) get(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		payments.JSON(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/payments/")
	if id == "" || strings.Contains(id, "/") {
		payments.JSON(w, 404, map[string]string{"error": "payment not found"})
		return
	}
	var out paymentLookup
	var created time.Time
	e := s.db.SQL.QueryRowContext(r.Context(), `SELECT id::text,idempotency_key,status,gateway,COALESCE(provider_ref,''),COALESCE(error,''),amount_minor,currency,instrument,debtor_participant_id,creditor_participant_id,reference,created_at FROM payments WHERE id=$1`, id).Scan(&out.ID, &out.IdempotencyKey, &out.Status, &out.Gateway, &out.ProviderRef, &out.Error, &out.AmountMinor, &out.Currency, &out.Instrument, &out.DebtorParticipantID, &out.CreditorParticipantID, &out.Reference, &created)
	if e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			payments.JSON(w, 404, map[string]string{"error": "payment not found"})
		} else {
			payments.JSON(w, 500, map[string]string{"error": "database read failed"})
		}
		return
	}
	out.Status = payments.NormalizeStatus(out.Status)
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
