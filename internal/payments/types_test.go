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

func TestValidateParticipantRolesAndInstrument(t *testing.T) {
	p := Participant{ID: "participant-1", Instrument: "card", Gateway: "gateway-card", Roles: []string{" debtor ", "creditor", "debtor"}}
	assert.NoError(t, ValidateParticipant(p))
	assert.Equal(t, []string{"debtor", "creditor"}, NormalizeRoles(p.Roles))
	assert.Error(t, ValidateParticipant(Participant{ID: "p", Instrument: "crypto", Gateway: "g", Roles: []string{"debtor"}}))
	assert.Error(t, ValidateParticipant(Participant{ID: "p", Instrument: "card", Gateway: "g", Roles: []string{"merchant"}}))
	assert.Error(t, ValidateParticipant(Participant{ID: "p", Instrument: "card", Gateway: "g"}))
}

func TestValidateCurrencyRequiresLetters(t *testing.T) {
	valid := PublicPaymentRequest{AmountMinor: 1, Currency: "cop", DebtorParticipantID: "d", CreditorParticipantID: "c", Reference: "r"}
	assert.NoError(t, ValidateRequest(valid))
	valid.Currency = "C0P"
	assert.Error(t, ValidateRequest(valid))
}

func TestValidateRouterRequestRejectsUnknownInstrument(t *testing.T) {
	req := RouterPaymentRequest{AmountMinor: 1, Currency: "COP", DebtorParticipantID: "d", CreditorParticipantID: "c", Reference: "r", Instrument: "unknown"}
	assert.Error(t, ValidateRouterRequest(req))
	req.Instrument = "bank_transfer"
	assert.NoError(t, ValidateRouterRequest(req))
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
