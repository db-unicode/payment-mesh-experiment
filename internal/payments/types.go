package payments

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// PublicPaymentRequest is the stable public contract. Instrument and gateway
// are deliberately absent: routing is derived from the debtor participant.
type PublicPaymentRequest struct {
	DebtorParticipantID   string `json:"debtor_participant_id"`
	CreditorParticipantID string `json:"creditor_participant_id"`
	AmountMinor           int64  `json:"amount_minor"`
	Currency              string `json:"currency"`
	Reference             string `json:"reference"`
}

// RouterPaymentRequest is an internal contract created by the operator after
// resolving both participants.
type RouterPaymentRequest struct {
	DebtorParticipantID   string `json:"debtor_participant_id"`
	CreditorParticipantID string `json:"creditor_participant_id"`
	AmountMinor           int64  `json:"amount_minor"`
	Currency              string `json:"currency"`
	Reference             string `json:"reference"`
	Instrument            string `json:"instrument"`
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

func RequestHash(req any) (string, error) {
	b, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

func ValidateRequest(req PublicPaymentRequest) error {
	if req.AmountMinor <= 0 {
		return fmt.Errorf("amount_minor must be positive")
	}
	if len(req.Currency) != 3 {
		return fmt.Errorf("currency must be ISO 4217")
	}
	if strings.TrimSpace(req.DebtorParticipantID) == "" {
		return fmt.Errorf("debtor_participant_id is required")
	}
	if strings.TrimSpace(req.CreditorParticipantID) == "" {
		return fmt.Errorf("creditor_participant_id is required")
	}
	if strings.TrimSpace(req.Reference) == "" {
		return fmt.Errorf("reference is required")
	}
	return nil
}

func ValidateRouterRequest(req RouterPaymentRequest) error {
	if req.AmountMinor <= 0 {
		return fmt.Errorf("amount_minor must be positive")
	}
	if len(req.Currency) != 3 {
		return fmt.Errorf("currency must be ISO 4217")
	}
	if strings.TrimSpace(req.Instrument) == "" {
		return fmt.Errorf("instrument is required")
	}
	if strings.TrimSpace(req.DebtorParticipantID) == "" || strings.TrimSpace(req.CreditorParticipantID) == "" {
		return fmt.Errorf("both participant IDs are required")
	}
	if strings.TrimSpace(req.Reference) == "" {
		return fmt.Errorf("reference is required")
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
