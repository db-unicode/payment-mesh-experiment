package payments

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateRequest(t *testing.T) {
	valid := PaymentRequest{AmountMinor: 100, Currency: "COP", Instrument: "card", ParticipantID: "participant-card"}
	assert.NoError(t, ValidateRequest(valid))
	assert.Error(t, ValidateRequest(PaymentRequest{AmountMinor: 0, Currency: "COP", Instrument: "card", ParticipantID: "p"}))
	assert.Error(t, ValidateRequest(PaymentRequest{AmountMinor: 100, Currency: "CO", Instrument: "card", ParticipantID: "p"}))
}

func TestRequestHashIsStableAndPayloadSensitive(t *testing.T) {
	request := PaymentRequest{AmountMinor: 100, Currency: "COP", Instrument: "card", ParticipantID: "p"}
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
