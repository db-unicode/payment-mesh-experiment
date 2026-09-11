package payments

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type PaymentRequest struct {
	AmountMinor   int64  `json:"amount_minor"`
	Currency      string `json:"currency"`
	Instrument    string `json:"instrument"`
	ParticipantID string `json:"participant_id"`
	Description   string `json:"description,omitempty"`
}

type PaymentResponse struct {
	ID             string `json:"id"`
	IdempotencyKey string `json:"idempotency_key"`
	Status         string `json:"status"`
	Gateway        string `json:"gateway,omitempty"`
	ProviderRef    string `json:"provider_ref,omitempty"`
	Error          string `json:"error,omitempty"`
	CreatedAt      string `json:"created_at"`
}

type Participant struct {
	ID         string `json:"id"`
	Instrument string `json:"instrument"`
	Gateway    string `json:"gateway"`
	Active     bool   `json:"active"`
}

func RequestHash(req PaymentRequest) (string, error) {
	b, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

func ValidateRequest(req PaymentRequest) error {
	if req.AmountMinor <= 0 {
		return fmt.Errorf("amount_minor must be positive")
	}
	if len(req.Currency) != 3 {
		return fmt.Errorf("currency must be ISO 4217")
	}
	if strings.TrimSpace(req.Instrument) == "" {
		return fmt.Errorf("instrument is required")
	}
	if strings.TrimSpace(req.ParticipantID) == "" {
		return fmt.Errorf("participant_id is required")
	}
	return nil
}

func NormalizeStatus(status string) string {
	switch status {
	case "SUCCEEDED", "FAILED", "PENDING":
		return status
	default:
		return "PENDING"
	}
}

func Now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
