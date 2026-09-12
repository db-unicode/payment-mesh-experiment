package payments

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCardAdapterNormalizesProviderStates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/card/authorizations", r.URL.Path)
		assert.Equal(t, "secret", r.Header.Get("X-Gateway-Auth"))
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"state":"AUTHORIZED","authorization_id":"auth-1"}`))
	}))
	defer server.Close()
	result := (CardAdapter{BaseURL: server.URL, Client: server.Client(), AuthToken: "secret"}).Charge(context.Background(), RouterPaymentRequest{AmountMinor: 1, Currency: "COP", Reference: "r"}, "key")
	assert.Equal(t, "SUCCEEDED", result.Status)
	assert.Equal(t, "auth-1", result.ProviderRef)
}

func TestBankAdapterNormalizesDeclineAndProviderFailure(t *testing.T) {
	declined := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"decision":"REJECTED","message":"blocked"}`))
	}))
	defer declined.Close()
	result := (BankAdapter{BaseURL: declined.URL, Client: declined.Client()}).Charge(context.Background(), RouterPaymentRequest{}, "key")
	assert.Equal(t, "FAILED", result.Status)
	assert.Equal(t, "bank transfer rejected", result.Error)

	failure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer failure.Close()
	result = (BankAdapter{BaseURL: failure.URL, Client: failure.Client()}).Charge(context.Background(), RouterPaymentRequest{}, "key")
	assert.Equal(t, "PENDING", result.Status)
	assert.Equal(t, "provider unavailable", result.Error)
}

func TestAdapterDoesNotExposeProviderErrorDetails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"internal account 12345"}`))
	}))
	defer server.Close()
	result := (CardAdapter{BaseURL: server.URL, Client: server.Client()}).Charge(context.Background(), RouterPaymentRequest{}, "key")
	assert.Equal(t, "FAILED", result.Status)
	assert.Equal(t, "provider rejected request", result.Error)
	assert.NotContains(t, result.Error, "12345")
}
