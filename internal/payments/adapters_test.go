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
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"state":"AUTHORIZED","authorization_id":"auth-1"}`))
	}))
	defer server.Close()
	result := (CardAdapter{BaseURL: server.URL, Client: server.Client()}).Charge(context.Background(), RouterPaymentRequest{AmountMinor: 1, Currency: "COP", Reference: "r"}, "key")
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

	failure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer failure.Close()
	result = (BankAdapter{BaseURL: failure.URL, Client: failure.Client()}).Charge(context.Background(), RouterPaymentRequest{}, "key")
	assert.Equal(t, "PENDING", result.Status)
}
