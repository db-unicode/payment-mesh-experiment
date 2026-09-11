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

type charge struct { AmountMinor int64 `json:"amount_minor"`; Currency string `json:"currency"`; Instrument string `json:"instrument"`; IdempotencyKey string `json:"idempotency_key"` }
type result struct { Status string `json:"status"`; ProviderRef string `json:"provider_ref,omitempty"`; Error string `json:"error,omitempty"` }
var state = struct { sync.Mutex; charges map[string]result }{charges: map[string]result{}}

func main(){mux:=http.NewServeMux();mux.HandleFunc("/healthz",health);mux.HandleFunc("/admin/behavior",behavior);mux.HandleFunc("/v1/charges",chargeHandler);addr:=getenv("HTTP_ADDR",":8080");log.Printf("gateway mock listening on %s (%s)",addr,getenv("GATEWAY_NAME","gateway"));log.Fatal(http.ListenAndServe(addr,mux))}
func health(w http.ResponseWriter,_ *http.Request){w.WriteHeader(200);_,_=w.Write([]byte(`{"status":"ok"}`))}
func chargeHandler(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{w.WriteHeader(405);return};var req charge;if json.NewDecoder(r.Body).Decode(&req)!=nil||req.IdempotencyKey==""{write(w,400,result{Status:"FAILED",Error:"invalid request"});return};state.Lock();if old,ok:=state.charges[req.IdempotencyKey];ok{state.Unlock();write(w,200,old);return};state.Unlock();behavior:=getenv("GATEWAY_BEHAVIOR","success");delay,_:=strconv.Atoi(getenv("GATEWAY_DELAY_MS","0"));if behavior=="timeout"{time.Sleep(10*time.Second)}else if delay>0{time.Sleep(time.Duration(delay)*time.Millisecond)};switch behavior{case "error":write(w,500,result{Status:"PENDING",Error:"simulated provider error"});return;case "decline":write(w,402,result{Status:"FAILED",Error:"simulated provider decline"});return};res:=result{Status:"SUCCEEDED",ProviderRef:fmt.Sprintf("%s-%d",getenv("GATEWAY_NAME","gateway"),time.Now().UnixNano())};state.Lock();state.charges[req.IdempotencyKey]=res;state.Unlock();write(w,200,res)}
func behavior(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{w.WriteHeader(405);return};var b struct{Behavior string `json:"behavior"`;DelayMs int `json:"delay_ms"`};if json.NewDecoder(r.Body).Decode(&b)!=nil||b.Behavior==""{write(w,400,map[string]string{"error":"behavior is required"});return};os.Setenv("GATEWAY_BEHAVIOR",b.Behavior);os.Setenv("GATEWAY_DELAY_MS",strconv.Itoa(b.DelayMs));write(w,200,map[string]string{"behavior":b.Behavior})}
func write(w http.ResponseWriter,status int,v any){w.Header().Set("Content-Type","application/json");w.WriteHeader(status);_=json.NewEncoder(w).Encode(v)}
func getenv(k,f string)string{if v:=os.Getenv(k);v!=""{return v};return f}
