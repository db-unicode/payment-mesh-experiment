package payments

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateRequest(t *testing.T) {
	valid := PublicPaymentRequest{AmountMinor: 100, Currency: "COP", DebtorParticipantID: "debtor", CreditorParticipantID: "creditor", Reference: "invoice-1"}
	assert.NoError(t, ValidateRequest(valid))
	assert.Error(t, ValidateRequest(PublicPaymentRequest{AmountMinor: 0, Currency: "COP", DebtorParticipantID: "d", CreditorParticipantID: "c", Reference: "r"}))
	assert.Error(t, ValidateRequest(PublicPaymentRequest{AmountMinor: 100, Currency: "CO", DebtorParticipantID: "d", CreditorParticipantID: "c", Reference: "r"}))
}

func TestRequestHashIsStableAndPayloadSensitive(t *testing.T) {
	request := PublicPaymentRequest{AmountMinor: 100, Currency: "COP", DebtorParticipantID: "d", CreditorParticipantID: "c", Reference: "r"}
	one, err := RequestHash(request)
	require.NoError(t, err)
	two, err := RequestHash(request)
	require.NoError(t, err)
	assert.Equal(t, one, two)
	request.AmountMinor = 101
	three, err := RequestHash(request)
	require.NoError(t, err)
	assert.NotEqual(t, one, three)
}

func TestNormalizeStatusPreservesUncertainty(t *testing.T) {
	assert.Equal(t, "SUCCEEDED", NormalizeStatus("SUCCEEDED"))
	assert.Equal(t, "FAILED", NormalizeStatus("FAILED"))
	assert.Equal(t, "PENDING", NormalizeStatus("gateway timeout"))
}

func TestTraceHeadersPropagateW3CAndRequestID(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("traceparent", "00-abc-def-01")
	req.Header.Set("tracestate", "vendor=value")
	req.Header.Set("x-request-id", "request-1")
	headers := TraceHeaders(req)
	assert.Equal(t, "00-abc-def-01", headers.Get("traceparent"))
	assert.Equal(t, "vendor=value", headers.Get("tracestate"))
	assert.Equal(t, "request-1", headers.Get("x-request-id"))
}
