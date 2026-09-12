package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/academic/payment-mesh-experiment/internal/payments"
	"github.com/stretchr/testify/require"
)

func integrationOperator(t *testing.T) (*server, func()) {
	t.Helper()
	url := os.Getenv("OPERATOR_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("OPERATOR_TEST_DATABASE_URL is not set")
	}
	t.Setenv("OPERATOR_DATABASE_URL", url)
	db, err := payments.Open(context.Background(), "OPERATOR_DATABASE_URL")
	require.NoError(t, err)
	require.NoError(t, db.EnsureOperator(context.Background()))
	return &server{db: db, client: &http.Client{}}, func() { _ = db.SQL.Close() }
}

func TestPublicReplayReconcilesPendingOrderAndUsesFrozenRouterRequest(t *testing.T) {
	s, cleanup := integrationOperator(t)
	defer cleanup()
	key := fmt.Sprintf("integration-operator-replay-%d", atomic.AddInt64(&operatorIntegrationSequence, 1))
	defer s.db.SQL.ExecContext(context.Background(), `DELETE FROM orders WHERE idempotency_key=$1`, key)
	const paymentID = "11111111-1111-4111-8111-111111111111"
	var routerPosts atomic.Int32
	var participantCalls atomic.Int32
	postedRequests := make([]payments.RouterPaymentRequest, 0, 2)
	s.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		path := r.URL.Path
		switch {
		case strings.HasPrefix(path, "/v1/participants/"):
			id := strings.TrimPrefix(path, "/v1/participants/")
			instrument, gateway := "card", "gateway-card"
			if participantCalls.Add(1) > 2 {
				// A replay must use the frozen router request instead of this
				// mutable participant record.
				instrument, gateway = "wallet", "gateway-card"
			}
			body := fmt.Sprintf(`{"id":%q,"instrument":%q,"gateway":%q,"roles":["debtor","creditor"],"active":true}`, id, instrument, gateway)
			return operatorResponse(http.StatusOK, body), nil
		case path == "/internal/v1/payments":
			call := routerPosts.Add(1)
			var posted payments.RouterPaymentRequest
			if err := json.NewDecoder(r.Body).Decode(&posted); err != nil {
				return nil, err
			}
			postedRequests = append(postedRequests, posted)
			status := `{"id":"` + paymentID + `","status":"PENDING","gateway":"gateway-card","error":"provider timeout"}`
			responseStatus := http.StatusAccepted
			if call == 2 {
				status = `{"id":"` + paymentID + `","status":"SUCCEEDED","gateway":"gateway-card","provider_ref":"provider-1"}`
				responseStatus = http.StatusOK
			}
			return operatorResponse(responseStatus, status), nil
		case path == "/v1/payments/"+paymentID:
			return operatorResponse(http.StatusAccepted, `{"id":"`+paymentID+`","status":"PENDING","gateway":"gateway-card","instrument":"card","debtor_participant_id":"debtor","creditor_participant_id":"creditor","amount_minor":100,"currency":"USD","reference":"same","error":"provider timeout"}`), nil
		default:
			return nil, fmt.Errorf("unexpected request %s %s", r.Method, path)
		}
	})

	request := payments.PublicPaymentRequest{DebtorParticipantID: "debtor", CreditorParticipantID: "creditor", AmountMinor: 100, Currency: "USD", Reference: "same"}
	body, err := json.Marshal(request)
	require.NoError(t, err)
	first := httptest.NewRequest(http.MethodPost, "/v1/payments", bytes.NewReader(body))
	first.Header.Set("Idempotency-Key", key)
	firstResponse := httptest.NewRecorder()
	s.create(firstResponse, first)
	require.Equal(t, http.StatusAccepted, firstResponse.Code)

	replay := httptest.NewRequest(http.MethodPost, "/v1/payments", bytes.NewReader(body))
	replay.Header.Set("Idempotency-Key", key)
	replayResponse := httptest.NewRecorder()
	s.create(replayResponse, replay)
	require.Equal(t, http.StatusOK, replayResponse.Code)
	var got payments.PaymentResponse
	require.NoError(t, json.Unmarshal(replayResponse.Body.Bytes(), &got))
	require.Equal(t, paymentID, got.ID)
	require.Equal(t, "SUCCEEDED", got.Status)
	require.Equal(t, int32(2), routerPosts.Load())
	require.Equal(t, int32(2), participantCalls.Load())
	require.Len(t, postedRequests, 2)
	require.Equal(t, postedRequests[0], postedRequests[1])
	require.Equal(t, payments.RouterPaymentRequest{DebtorParticipantID: "debtor", CreditorParticipantID: "creditor", AmountMinor: 100, Currency: "USD", Reference: "same", Instrument: "card"}, postedRequests[0])

	var stored string
	require.NoError(t, s.db.SQL.QueryRowContext(context.Background(), `SELECT status FROM orders WHERE idempotency_key=$1`, key).Scan(&stored))
	require.Equal(t, "SUCCEEDED", stored)
}

var operatorIntegrationSequence int64

func operatorResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}
