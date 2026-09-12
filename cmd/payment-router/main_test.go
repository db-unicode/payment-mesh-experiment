package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/academic/payment-mesh-experiment/internal/payments"
	"github.com/stretchr/testify/require"
)

func TestChargeCallsProviderOnceAndReusesProviderKey(t *testing.T) {
	var calls int
	var gotKey string
	s := &server{
		circuits:       map[string]*payments.Circuit{"gateway-card": payments.NewCircuit(2, time.Second)},
		breakerEnabled: false,
		adapters: map[string]func(context.Context, payments.RouterPaymentRequest, string) payments.ProviderResult{
			"gateway-card": func(_ context.Context, _ payments.RouterPaymentRequest, key string) payments.ProviderResult {
				calls++
				gotKey = key
				return payments.ProviderResult{Status: "SUCCEEDED", ProviderRef: "provider-1"}
			},
		},
	}

	result := s.charge(httptest.NewRequest(http.MethodPost, "/", nil), payments.RouterPaymentRequest{Instrument: "card"}, "idem-1", "gateway-card")
	require.Equal(t, "SUCCEEDED", result.Status)
	require.Equal(t, 1, calls)
	require.Equal(t, "idem-1", gotKey)
}

func TestPendingRecoveryAfterHasSafeDefaultAndRejectsInvalidValues(t *testing.T) {
	t.Setenv("PENDING_RECOVERY_AFTER", "")
	require.Equal(t, defaultPendingRecoveryAfter, pendingRecoveryAfter())
	t.Setenv("PENDING_RECOVERY_AFTER", "not-a-duration")
	require.Equal(t, defaultPendingRecoveryAfter, pendingRecoveryAfter())
	t.Setenv("PENDING_RECOVERY_AFTER", "-1s")
	require.Equal(t, defaultPendingRecoveryAfter, pendingRecoveryAfter())
	t.Setenv("PENDING_RECOVERY_AFTER", "5s")
	require.Equal(t, 5*time.Second, pendingRecoveryAfter())
}
