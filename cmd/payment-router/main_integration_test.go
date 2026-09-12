package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/academic/payment-mesh-experiment/internal/payments"
	"github.com/stretchr/testify/require"
)

// These tests use PostgreSQL when ROUTER_TEST_DATABASE_URL is supplied. They
// remain opt-in so unit tests do not require a local database.
func integrationRouter(t *testing.T) (*server, func()) {
	t.Helper()
	url := os.Getenv("ROUTER_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("ROUTER_TEST_DATABASE_URL is not set")
	}
	t.Setenv("ROUTER_DATABASE_URL", url)
	db, err := payments.Open(context.Background(), "ROUTER_DATABASE_URL")
	require.NoError(t, err)
	require.NoError(t, db.EnsurePayments(context.Background()))
	return &server{db: db, circuits: map[string]*payments.Circuit{"gateway-card": payments.NewCircuit(5, time.Second)}, breakerEnabled: false}, func() { _ = db.SQL.Close() }
}

func TestCreateConcurrentDuplicatesUseOneProviderCall(t *testing.T) {
	s, cleanup := integrationRouter(t)
	defer cleanup()
	key := fmt.Sprintf("integration-concurrent-%d", time.Now().UnixNano())
	defer s.db.SQL.ExecContext(context.Background(), `DELETE FROM payments WHERE idempotency_key=$1`, key)
	var calls atomic.Int32
	s.adapters = map[string]func(context.Context, payments.RouterPaymentRequest, string) payments.ProviderResult{
		"gateway-card": func(_ context.Context, _ payments.RouterPaymentRequest, gotKey string) payments.ProviderResult {
			require.Equal(t, key, gotKey)
			calls.Add(1)
			return payments.ProviderResult{Status: "SUCCEEDED", ProviderRef: "provider-1"}
		},
	}
	req := payments.RouterPaymentRequest{DebtorParticipantID: "debtor", CreditorParticipantID: "creditor", AmountMinor: 100, Currency: "USD", Reference: "same", Instrument: "card"}
	body, err := json.Marshal(req)
	require.NoError(t, err)
	const duplicates = 10
	responses := make([]*httptest.ResponseRecorder, duplicates)
	var wg sync.WaitGroup
	for i := range responses {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := httptest.NewRequest(http.MethodPost, "/internal/v1/payments", bytes.NewReader(body))
			r.Header.Set("Idempotency-Key", key)
			w := httptest.NewRecorder()
			s.create(w, r)
			responses[i] = w
		}(i)
	}
	wg.Wait()
	require.Equal(t, int32(1), calls.Load())
	var first payments.PaymentResponse
	require.NoError(t, json.Unmarshal(responses[0].Body.Bytes(), &first))
	for _, response := range responses {
		require.Equal(t, http.StatusOK, response.Code)
		var got payments.PaymentResponse
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &got))
		require.Equal(t, first, got)
	}
}

func TestCreateRecoversStalePendingWithSameProviderKey(t *testing.T) {
	s, cleanup := integrationRouter(t)
	defer cleanup()
	key := fmt.Sprintf("integration-recovery-%d", time.Now().UnixNano())
	defer s.db.SQL.ExecContext(context.Background(), `DELETE FROM payments WHERE idempotency_key=$1`, key)
	id := newUUID()
	req := payments.RouterPaymentRequest{DebtorParticipantID: "debtor", CreditorParticipantID: "creditor", AmountMinor: 100, Currency: "usd", Reference: "recover", Instrument: "card"}
	canonical := req
	canonical.Currency = "USD"
	hash, err := payments.RequestHash(canonical)
	require.NoError(t, err)
	_, err = s.db.SQL.ExecContext(context.Background(), `INSERT INTO payments(id,idempotency_key,request_hash,amount_minor,currency,instrument,participant_id,debtor_participant_id,creditor_participant_id,reference,status,gateway,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'PENDING',$11,now()-interval '1 hour')`, id, key, hash, req.AmountMinor, canonical.Currency, req.Instrument, req.DebtorParticipantID, req.DebtorParticipantID, req.CreditorParticipantID, req.Reference, "gateway-card")
	require.NoError(t, err)
	var calls atomic.Int32
	s.adapters = map[string]func(context.Context, payments.RouterPaymentRequest, string) payments.ProviderResult{
		"gateway-card": func(_ context.Context, got payments.RouterPaymentRequest, gotKey string) payments.ProviderResult {
			require.Equal(t, canonical, got)
			require.Equal(t, key, gotKey)
			calls.Add(1)
			return payments.ProviderResult{Status: "SUCCEEDED", ProviderRef: "provider-recovered"}
		},
	}
	body, err := json.Marshal(req)
	require.NoError(t, err)
	r := httptest.NewRequest(http.MethodPost, "/internal/v1/payments", bytes.NewReader(body))
	r.Header.Set("Idempotency-Key", key)
	w := httptest.NewRecorder()
	s.create(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, int32(1), calls.Load())
	var got payments.PaymentResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Equal(t, id, got.ID)
	require.Equal(t, "SUCCEEDED", got.Status)
}

func TestCreateRecoversLegacyLowercaseHashWithCanonicalReplay(t *testing.T) {
	s, cleanup := integrationRouter(t)
	defer cleanup()
	key := fmt.Sprintf("integration-legacy-hash-%d", time.Now().UnixNano())
	defer s.db.SQL.ExecContext(context.Background(), `DELETE FROM payments WHERE idempotency_key=$1`, key)
	id := newUUID()
	req := payments.RouterPaymentRequest{DebtorParticipantID: "debtor", CreditorParticipantID: "creditor", AmountMinor: 100, Currency: "USD", Reference: "legacy", Instrument: "card"}
	legacy := req
	legacy.Currency = "usd"
	legacyHash, err := payments.RequestHash(legacy)
	require.NoError(t, err)
	_, err = s.db.SQL.ExecContext(context.Background(), `INSERT INTO payments(id,idempotency_key,request_hash,amount_minor,currency,instrument,participant_id,debtor_participant_id,creditor_participant_id,reference,status,gateway,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'PENDING',$11,now()-interval '1 hour')`, id, key, legacyHash, req.AmountMinor, req.Currency, req.Instrument, req.DebtorParticipantID, req.DebtorParticipantID, req.CreditorParticipantID, req.Reference, "gateway-card")
	require.NoError(t, err)
	var calls atomic.Int32
	s.adapters = map[string]func(context.Context, payments.RouterPaymentRequest, string) payments.ProviderResult{
		"gateway-card": func(_ context.Context, got payments.RouterPaymentRequest, gotKey string) payments.ProviderResult {
			require.Equal(t, req, got)
			require.Equal(t, key, gotKey)
			calls.Add(1)
			return payments.ProviderResult{Status: "SUCCEEDED", ProviderRef: "provider-legacy"}
		},
	}
	body, err := json.Marshal(req)
	require.NoError(t, err)
	t.Setenv("PENDING_RECOVERY_AFTER", "0s")
	r := httptest.NewRequest(http.MethodPost, "/internal/v1/payments", bytes.NewReader(body))
	r.Header.Set("Idempotency-Key", key)
	w := httptest.NewRecorder()
	s.create(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, int32(1), calls.Load())
	var got payments.PaymentResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Equal(t, id, got.ID)
	require.Equal(t, "SUCCEEDED", got.Status)
}

func TestFailedResultUpdateLeavesDurablePendingForSafeReplay(t *testing.T) {
	s, cleanup := integrationRouter(t)
	defer cleanup()
	key := fmt.Sprintf("integration-update-failure-%d", time.Now().UnixNano())
	defer s.db.SQL.ExecContext(context.Background(), `DELETE FROM payments WHERE idempotency_key=$1`, key)
	req := payments.RouterPaymentRequest{DebtorParticipantID: "debtor", CreditorParticipantID: "creditor", AmountMinor: 100, Currency: "USD", Reference: "update-failure", Instrument: "card"}
	body, err := json.Marshal(req)
	require.NoError(t, err)
	var providerCalls, effectiveCharges atomic.Int32
	var providerCharged atomic.Bool
	s.adapters = map[string]func(context.Context, payments.RouterPaymentRequest, string) payments.ProviderResult{
		"gateway-card": func(_ context.Context, _ payments.RouterPaymentRequest, gotKey string) payments.ProviderResult {
			providerCalls.Add(1)
			if providerCharged.CompareAndSwap(false, true) {
				effectiveCharges.Add(1)
				_, updateErr := s.db.SQL.ExecContext(context.Background(), `UPDATE payments SET updated_at=now()+interval '1 second' WHERE idempotency_key=$1`, gotKey)
				require.NoError(t, updateErr)
			}
			return payments.ProviderResult{Status: "SUCCEEDED", ProviderRef: "provider-deduplicated"}
		},
	}
	first := httptest.NewRequest(http.MethodPost, "/internal/v1/payments", bytes.NewReader(body))
	first.Header.Set("Idempotency-Key", key)
	firstResponse := httptest.NewRecorder()
	s.create(firstResponse, first)
	require.Equal(t, http.StatusAccepted, firstResponse.Code)
	var firstPayment payments.PaymentResponse
	require.NoError(t, json.Unmarshal(firstResponse.Body.Bytes(), &firstPayment))
	require.Equal(t, "PENDING", firstPayment.Status)
	var storedID, storedStatus string
	require.NoError(t, s.db.SQL.QueryRowContext(context.Background(), `SELECT id::text,status FROM payments WHERE idempotency_key=$1`, key).Scan(&storedID, &storedStatus))
	require.Equal(t, firstPayment.ID, storedID)
	require.Equal(t, "PENDING", storedStatus)
	_, err = s.db.SQL.ExecContext(context.Background(), `UPDATE payments SET updated_at=now()-interval '1 hour' WHERE idempotency_key=$1`, key)
	require.NoError(t, err)
	t.Setenv("PENDING_RECOVERY_AFTER", "0s")
	replay := httptest.NewRequest(http.MethodPost, "/internal/v1/payments", bytes.NewReader(body))
	replay.Header.Set("Idempotency-Key", key)
	replayResponse := httptest.NewRecorder()
	s.create(replayResponse, replay)
	require.Equal(t, http.StatusOK, replayResponse.Code)
	var replayPayment payments.PaymentResponse
	require.NoError(t, json.Unmarshal(replayResponse.Body.Bytes(), &replayPayment))
	require.Equal(t, storedID, replayPayment.ID)
	require.Equal(t, "SUCCEEDED", replayPayment.Status)
	require.Equal(t, int32(2), providerCalls.Load())
	require.Equal(t, int32(1), effectiveCharges.Load(), "provider idempotency key must prevent a second effective charge")
}

func TestCreateRejectsPayloadConflictForReusedKey(t *testing.T) {
	s, cleanup := integrationRouter(t)
	defer cleanup()
	key := fmt.Sprintf("integration-conflict-%d", time.Now().UnixNano())
	defer s.db.SQL.ExecContext(context.Background(), `DELETE FROM payments WHERE idempotency_key=$1`, key)
	req := payments.RouterPaymentRequest{DebtorParticipantID: "debtor", CreditorParticipantID: "creditor", AmountMinor: 100, Currency: "USD", Reference: "original", Instrument: "card"}
	s.adapters = map[string]func(context.Context, payments.RouterPaymentRequest, string) payments.ProviderResult{
		"gateway-card": func(context.Context, payments.RouterPaymentRequest, string) payments.ProviderResult {
			return payments.ProviderResult{Status: "SUCCEEDED", ProviderRef: "provider-1"}
		},
	}
	body, err := json.Marshal(req)
	require.NoError(t, err)
	first := httptest.NewRequest(http.MethodPost, "/internal/v1/payments", bytes.NewReader(body))
	first.Header.Set("Idempotency-Key", key)
	firstResponse := httptest.NewRecorder()
	s.create(firstResponse, first)
	require.Equal(t, http.StatusOK, firstResponse.Code)
	req.Reference = "changed"
	body, err = json.Marshal(req)
	require.NoError(t, err)
	conflict := httptest.NewRequest(http.MethodPost, "/internal/v1/payments", bytes.NewReader(body))
	conflict.Header.Set("Idempotency-Key", key)
	conflictResponse := httptest.NewRecorder()
	s.create(conflictResponse, conflict)
	require.Equal(t, http.StatusConflict, conflictResponse.Code)
}
