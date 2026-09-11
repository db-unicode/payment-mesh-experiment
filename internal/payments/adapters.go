package payments

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type ProviderResult struct{ Status, ProviderRef, Error string }

type CardAdapter struct {
	BaseURL string
	Client  *http.Client
}
type BankAdapter struct {
	BaseURL string
	Client  *http.Client
}

type cardRequest struct {
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
	Reference   string `json:"reference"`
	RequestID   string `json:"request_id"`
}
type cardResponse struct {
	State           string `json:"state"`
	AuthorizationID string `json:"authorization_id"`
	Message         string `json:"message"`
}
type bankRequest struct {
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
	Reference   string `json:"reference"`
	TransferID  string `json:"transfer_id"`
}
type bankResponse struct {
	Decision   string `json:"decision"`
	TransferID string `json:"transfer_id"`
	Message    string `json:"message"`
}

func NewProviderClient(timeoutSeconds int, insecureTLS bool) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: insecureTLS} // local self-signed mocks only
	return &http.Client{Transport: transport, Timeout: timeDuration(timeoutSeconds)}
}

func (a CardAdapter) Charge(ctx context.Context, req RouterPaymentRequest, key string) ProviderResult {
	payload, _ := json.Marshal(cardRequest{AmountMinor: req.AmountMinor, Currency: req.Currency, Reference: req.Reference, RequestID: key})
	return callProvider(ctx, a.Client, strings.TrimRight(a.BaseURL, "/")+"/v1/card/authorizations", payload, func(body []byte) ProviderResult {
		var out cardResponse
		if json.Unmarshal(body, &out) != nil {
			return ProviderResult{Status: "PENDING", Error: "invalid card provider response"}
		}
		switch strings.ToUpper(out.State) {
		case "AUTHORIZED":
			return ProviderResult{Status: "SUCCEEDED", ProviderRef: out.AuthorizationID}
		case "DECLINED":
			return ProviderResult{Status: "FAILED", ProviderRef: out.AuthorizationID, Error: out.Message}
		default:
			return ProviderResult{Status: "PENDING", Error: out.Message}
		}
	})
}

func (a BankAdapter) Charge(ctx context.Context, req RouterPaymentRequest, key string) ProviderResult {
	payload, _ := json.Marshal(bankRequest{AmountMinor: req.AmountMinor, Currency: req.Currency, Reference: req.Reference, TransferID: key})
	return callProvider(ctx, a.Client, strings.TrimRight(a.BaseURL, "/")+"/v2/transfers", payload, func(body []byte) ProviderResult {
		var out bankResponse
		if json.Unmarshal(body, &out) != nil {
			return ProviderResult{Status: "PENDING", Error: "invalid bank provider response"}
		}
		switch strings.ToUpper(out.Decision) {
		case "ACCEPTED":
			return ProviderResult{Status: "SUCCEEDED", ProviderRef: out.TransferID}
		case "REJECTED":
			return ProviderResult{Status: "FAILED", ProviderRef: out.TransferID, Error: out.Message}
		default:
			return ProviderResult{Status: "PENDING", Error: out.Message}
		}
	})
}

func callProvider(ctx context.Context, client *http.Client, url string, body []byte, normalize func([]byte) ProviderResult) ProviderResult {
	if client == nil {
		client = &http.Client{}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return ProviderResult{Status: "PENDING", Error: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	for name, values := range TraceHeadersFromContext(ctx) {
		req.Header[name] = values
	}
	resp, err := client.Do(req)
	if err != nil {
		return ProviderResult{Status: "PENDING", Error: err.Error()}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusGatewayTimeout {
		return ProviderResult{Status: "PENDING", Error: fmt.Sprintf("provider status %d", resp.StatusCode)}
	}
	if resp.StatusCode >= 400 {
		return ProviderResult{Status: "FAILED", Error: fmt.Sprintf("provider status %d", resp.StatusCode)}
	}
	return normalize(data)
}

func timeDuration(seconds int) time.Duration { return time.Duration(seconds) * time.Second }
