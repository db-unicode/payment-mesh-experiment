package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/academic/payment-mesh-experiment/internal/payments"
)

type server struct {
	db      *payments.DB
	metrics payments.Metrics
}

func main() {
	ctx := context.Background()
	db, err := openWithRetry(ctx, "PARTICIPANT_DATABASE_URL")
	if err != nil {
		log.Fatal(err)
	}
	defer db.SQL.Close()
	if err := db.EnsureParticipants(ctx); err != nil {
		log.Fatal(err)
	}
	if err := seed(ctx, db); err != nil {
		log.Fatal(err)
	}
	s := &server{db: db}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.health)
	mux.HandleFunc("/readyz", s.ready)
	mux.HandleFunc("/metrics", s.metrics.Handler)
	mux.HandleFunc("/v1/participants/", s.routing)
	mux.HandleFunc("/v1/participants", s.create)
	addr := getenv("HTTP_ADDR", ":8080")
	log.Printf("participant-payment-manager listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func openWithRetry(ctx context.Context, name string) (*payments.DB, error) {
	var last error
	for i := 0; i < 30; i++ {
		db, err := payments.Open(ctx, name)
		if err == nil {
			return db, nil
		}
		last = err
		time.Sleep(time.Second)
	}
	return nil, last
}
func seed(ctx context.Context, db *payments.DB) error {
	_, err := db.SQL.ExecContext(ctx, `INSERT INTO participants (id,instrument,gateway) VALUES
	('participant-card','card','gateway-card'),('participant-bank','bank_transfer','gateway-bank'),('participant-wallet','wallet','gateway-card')
	ON CONFLICT (id) DO NOTHING`)
	return err
}
func (s *server) health(w http.ResponseWriter, _ *http.Request) {
	payments.JSON(w, 200, map[string]string{"status": "ok"})
}
func (s *server) ready(w http.ResponseWriter, _ *http.Request) {
	if err := s.db.SQL.Ping(); err != nil {
		payments.JSON(w, 503, map[string]string{"status": "not_ready"})
		return
	}
	payments.JSON(w, 200, map[string]string{"status": "ready"})
}
func (s *server) routing(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	id := strings.TrimPrefix(r.URL.Path, "/v1/participants/")
	if r.Method != http.MethodGet || id == "" {
		payments.JSON(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	var p payments.Participant
	err := s.db.SQL.QueryRowContext(r.Context(), `SELECT id,instrument,gateway,active FROM participants WHERE id=$1`, id).Scan(&p.ID, &p.Instrument, &p.Gateway, &p.Active)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			payments.JSON(w, 404, map[string]string{"error": "participant not found"})
		} else {
			payments.JSON(w, 503, map[string]string{"error": "participant database unavailable"})
		}
		return
	}
	if !p.Active {
		payments.JSON(w, 409, map[string]string{"error": "participant inactive"})
		return
	}
	s.metrics.Count(true, time.Since(started).Milliseconds())
	payments.JSON(w, 200, p)
}
func (s *server) create(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		payments.JSON(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	var p payments.Participant
	if err := payments.Decode(r, &p); err != nil || p.ID == "" || p.Instrument == "" || p.Gateway == "" {
		payments.JSON(w, 400, map[string]string{"error": "id, instrument and gateway are required"})
		return
	}
	_, err := s.db.SQL.ExecContext(r.Context(), `INSERT INTO participants(id,instrument,gateway,active) VALUES($1,$2,$3,$4) ON CONFLICT(id) DO UPDATE SET instrument=EXCLUDED.instrument,gateway=EXCLUDED.gateway,active=EXCLUDED.active`, p.ID, p.Instrument, p.Gateway, p.Active)
	if err != nil {
		payments.JSON(w, 500, map[string]string{"error": "could not save participant"})
		return
	}
	payments.JSON(w, 201, p)
}
func getenv(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}
