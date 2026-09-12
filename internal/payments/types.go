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
	if !validCurrency(req.Currency) {
		return fmt.Errorf("currency must be 3 letters")
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
	if !validCurrency(req.Currency) {
		return fmt.Errorf("currency must be 3 letters")
	}
	if !ValidInstrument(req.Instrument) {
		return fmt.Errorf("instrument is unsupported")
	}
	if strings.TrimSpace(req.DebtorParticipantID) == "" || strings.TrimSpace(req.CreditorParticipantID) == "" {
		return fmt.Errorf("both participant IDs are required")
	}
	if strings.TrimSpace(req.Reference) == "" {
		return fmt.Errorf("reference is required")
	}
	return nil
}

// Participant roles describe which side(s) of a payment a participant may
// occupy. A participant may have both roles (the seeded test participants do).
const (
	RoleDebtor   = "debtor"
	RoleCreditor = "creditor"
)

var validRoles = map[string]struct{}{
	RoleDebtor: {}, RoleCreditor: {},
}

// Participant is the routing record shared by the participant manager and
// operator. Roles are persisted as a set, and are deliberately part of the
// contract returned to the operator so it can authorize each payment side.
type Participant struct {
	ID         string   `json:"id"`
	Instrument string   `json:"instrument"`
	Gateway    string   `json:"gateway"`
	Roles      []string `json:"roles"`
	Active     bool     `json:"active"`
}

func ValidInstrument(instrument string) bool {
	// Internal routing receives the canonical value persisted by the participant
	// manager. Rejecting aliases/whitespace here prevents gatewayFor from
	// silently routing an invalid value to the card gateway.
	if instrument != strings.TrimSpace(instrument) || instrument != strings.ToLower(instrument) {
		return false
	}
	switch instrument {
	case "card", "bank_transfer", "wallet":
		return true
	default:
		return false
	}
}

func NormalizeRoles(roles []string) []string {
	seen := make(map[string]struct{}, len(roles))
	out := make([]string, 0, len(roles))
	for _, role := range roles {
		role = strings.ToLower(strings.TrimSpace(role))
		if role == "" {
			continue
		}
		if _, ok := seen[role]; ok {
			continue
		}
		seen[role] = struct{}{}
		out = append(out, role)
	}
	return out
}

func ValidateParticipant(p Participant) error {
	if strings.TrimSpace(p.ID) == "" {
		return fmt.Errorf("id is required")
	}
	if !ValidInstrument(p.Instrument) {
		return fmt.Errorf("instrument is unsupported")
	}
	if strings.TrimSpace(p.Gateway) == "" {
		return fmt.Errorf("gateway is required")
	}
	roles := NormalizeRoles(p.Roles)
	if len(roles) == 0 {
		return fmt.Errorf("at least one role is required")
	}
	for _, role := range roles {
		if _, ok := validRoles[role]; !ok {
			return fmt.Errorf("unsupported participant role: %s", role)
		}
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

func validCurrency(currency string) bool {
	currency = strings.TrimSpace(currency)
	if len(currency) != 3 {
		return false
	}
	for _, c := range currency {
		if c < 'A' || c > 'Z' {
			if c < 'a' || c > 'z' {
				return false
			}
		}
	}
	return true
}

func Now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
