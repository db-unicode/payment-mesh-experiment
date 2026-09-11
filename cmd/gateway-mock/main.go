package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

type cardRequest struct {
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
	Reference   string `json:"reference"`
	RequestID   string `json:"request_id"`
}
type bankRequest struct {
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
	Reference   string `json:"reference"`
	TransferID  string `json:"transfer_id"`
}
type cardResult struct {
	State           string `json:"state"`
	AuthorizationID string `json:"authorization_id,omitempty"`
	Message         string `json:"message,omitempty"`
}
type bankResult struct {
	Decision   string `json:"decision"`
	TransferID string `json:"transfer_id,omitempty"`
	Message    string `json:"message,omitempty"`
}
type stats struct {
	Attempts         int `json:"attempts"`
	EffectiveCharges int `json:"effective_charges"`
}

var state = struct {
	sync.Mutex
	card  map[string]cardResult
	bank  map[string]bankResult
	stats stats
}{card: map[string]cardResult{}, bank: map[string]bankResult{}}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", health)
	mux.HandleFunc("/admin/behavior", behavior)
	mux.HandleFunc("/stats", statsHandler)
	mux.HandleFunc("/v1/card/authorizations", cardAuthorization)
	mux.HandleFunc("/v2/transfers", bankTransfer)
	addr := getenv("HTTP_ADDR", ":8080")
	log.Printf("gateway mock listening on %s (%s)", addr, getenv("GATEWAY_NAME", "gateway"))
	if cert, key := os.Getenv("TLS_CERT_FILE"), os.Getenv("TLS_KEY_FILE"); cert != "" && key != "" {
		log.Fatal(http.ListenAndServeTLS(addr, cert, key, mux))
	} else {
		log.Fatal(http.ListenAndServe(addr, mux))
	}
}
func health(w http.ResponseWriter, _ *http.Request) { write(w, 200, map[string]string{"status": "ok"}) }
func cardAuthorization(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		write(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	var req cardRequest
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.RequestID == "" {
		write(w, 400, map[string]string{"error": "request_id is required"})
		return
	}
	state.Lock()
	state.stats.Attempts++
	if old, ok := state.card[req.RequestID]; ok {
		state.Unlock()
		write(w, 200, old)
		return
	}
	state.Unlock()
	if waitProvider(r) {
		return
	}
	behavior := getenv("GATEWAY_BEHAVIOR", "success")
	if behavior == "error" {
		write(w, 503, map[string]string{"message": "provider unavailable"})
		return
	}
	if behavior == "decline" {
		res := cardResult{State: "DECLINED", Message: "card authorization declined"}
		state.Lock()
		if old, ok := state.card[req.RequestID]; ok {
			state.Unlock()
			write(w, 200, old)
			return
		}
		state.card[req.RequestID] = res
		state.stats.EffectiveCharges++
		state.Unlock()
		write(w, 200, res)
		return
	}
	res := cardResult{State: "AUTHORIZED", AuthorizationID: fmt.Sprintf("card-auth-%d", time.Now().UnixNano())}
	state.Lock()
	if old, ok := state.card[req.RequestID]; ok {
		state.Unlock()
		write(w, 200, old)
		return
	}
	state.card[req.RequestID] = res
	state.stats.EffectiveCharges++
	state.Unlock()
	write(w, 200, res)
}
func bankTransfer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		write(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	var req bankRequest
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.TransferID == "" {
		write(w, 400, map[string]string{"error": "transfer_id is required"})
		return
	}
	state.Lock()
	state.stats.Attempts++
	if old, ok := state.bank[req.TransferID]; ok {
		state.Unlock()
		write(w, 200, old)
		return
	}
	state.Unlock()
	if waitProvider(r) {
		return
	}
	behavior := getenv("GATEWAY_BEHAVIOR", "success")
	if behavior == "error" {
		write(w, 503, map[string]string{"message": "bank provider unavailable"})
		return
	}
	if behavior == "decline" {
		res := bankResult{Decision: "REJECTED", Message: "bank transfer rejected"}
		state.Lock()
		if old, ok := state.bank[req.TransferID]; ok {
			state.Unlock()
			write(w, 200, old)
			return
		}
		state.bank[req.TransferID] = res
		state.stats.EffectiveCharges++
		state.Unlock()
		write(w, 200, res)
		return
	}
	res := bankResult{Decision: "ACCEPTED", TransferID: fmt.Sprintf("bank-transfer-%d", time.Now().UnixNano())}
	state.Lock()
	if old, ok := state.bank[req.TransferID]; ok {
		state.Unlock()
		write(w, 200, old)
		return
	}
	state.bank[req.TransferID] = res
	state.stats.EffectiveCharges++
	state.Unlock()
	write(w, 200, res)
}
func waitProvider(r *http.Request) bool {
	delay, _ := strconv.Atoi(getenv("GATEWAY_DELAY_MS", "0"))
	if getenv("GATEWAY_BEHAVIOR", "success") == "timeout" {
		delay = 10000
	}
	if delay <= 0 {
		return false
	}
	timer := time.NewTimer(time.Duration(delay) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
		return false
	case <-r.Context().Done():
		return true
	}
}
func behavior(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		write(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	var b struct {
		Behavior string `json:"behavior"`
		DelayMs  int    `json:"delay_ms"`
	}
	if json.NewDecoder(r.Body).Decode(&b) != nil || b.Behavior == "" {
		write(w, 400, map[string]string{"error": "behavior is required"})
		return
	}
	os.Setenv("GATEWAY_BEHAVIOR", b.Behavior)
	os.Setenv("GATEWAY_DELAY_MS", strconv.Itoa(b.DelayMs))
	write(w, 200, b)
}
func statsHandler(w http.ResponseWriter, _ *http.Request) {
	state.Lock()
	defer state.Unlock()
	write(w, 200, state.stats)
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func getenv(k, f string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return f
}
