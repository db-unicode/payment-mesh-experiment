package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/academic/payment-mesh-experiment/internal/payments"
	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchRouterForwardsTraceHeadersAndNormalizesStatus(t *testing.T) {
	var gotTraceparent, gotRequestID string
	s := &server{client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotTraceparent = r.Header.Get("traceparent")
		gotRequestID = r.Header.Get("x-request-id")
		return &http.Response{
			StatusCode: http.StatusAccepted,
			Body:       io.NopCloser(strings.NewReader(`{"id":"payment-1","status":"provider-unknown"}`)),
			Header:     make(http.Header),
		}, nil
	})}}

	result, err := s.fetchRouter(context.Background(), "payment-1", http.Header{
		"Traceparent":  []string{"00-abc-def-01"},
		"X-Request-Id": []string{"request-1"},
	})
	require.NoError(t, err)
	require.Equal(t, "PENDING", result.Status)
	require.Equal(t, "00-abc-def-01", gotTraceparent)
	require.Equal(t, "request-1", gotRequestID)
}

func TestFetchRouterRejectsTerminalHTTPError(t *testing.T) {
	s := &server{client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader(`{"error":"missing"}`)),
			Header:     make(http.Header),
		}, nil
	})}}
	_, err := s.fetchRouter(context.Background(), "missing", nil)
	require.Error(t, err)
}

func TestStatusCodeUsesNormalizedOperatorState(t *testing.T) {
	require.Equal(t, http.StatusAccepted, payments.StatusCode(payments.NormalizeStatus("unknown")))
}

func TestHasRoleChecksPaymentSide(t *testing.T) {
	p := payments.Participant{Roles: []string{" creditor "}}
	require.True(t, hasRole(p, payments.RoleCreditor))
	require.False(t, hasRole(p, payments.RoleDebtor))
}
