package payments

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type DB struct{ SQL *sql.DB }

func Open(ctx context.Context, envName string) (*DB, error) {
	url := os.Getenv(envName)
	if url == "" {
		return nil, fmt.Errorf("%s is required", envName)
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxIdleTime(5 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &DB{SQL: db}, nil
}

func (d *DB) EnsureParticipants(ctx context.Context) error {
	_, err := d.SQL.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS participants (
		id TEXT PRIMARY KEY, instrument TEXT NOT NULL, gateway TEXT NOT NULL,
		roles JSONB NOT NULL DEFAULT '[]'::jsonb,
		active BOOLEAN NOT NULL DEFAULT TRUE,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now());
		ALTER TABLE participants ADD COLUMN IF NOT EXISTS roles JSONB NOT NULL DEFAULT '[]'::jsonb;
		ALTER TABLE participants ALTER COLUMN roles SET DEFAULT '[]'::jsonb`)
	return err
}

func (d *DB) EnsurePayments(ctx context.Context) error {
	_, err := d.SQL.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS payments (
		id UUID PRIMARY KEY, idempotency_key TEXT NOT NULL UNIQUE, request_hash TEXT NOT NULL,
		amount_minor BIGINT NOT NULL, currency CHAR(3) NOT NULL, instrument TEXT NOT NULL,
		participant_id TEXT NOT NULL, debtor_participant_id TEXT NOT NULL DEFAULT '', creditor_participant_id TEXT NOT NULL DEFAULT '', reference TEXT NOT NULL DEFAULT '', status TEXT NOT NULL, gateway TEXT NOT NULL,
		provider_ref TEXT, error TEXT, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now());
		ALTER TABLE payments ADD COLUMN IF NOT EXISTS debtor_participant_id TEXT NOT NULL DEFAULT '';
		ALTER TABLE payments ADD COLUMN IF NOT EXISTS creditor_participant_id TEXT NOT NULL DEFAULT '';
		ALTER TABLE payments ADD COLUMN IF NOT EXISTS reference TEXT NOT NULL DEFAULT '';
		CREATE INDEX IF NOT EXISTS payments_status_idx ON payments(status)`)
	return err
}

func (d *DB) EnsureOperator(ctx context.Context) error {
	_, err := d.SQL.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS orders (
		id UUID PRIMARY KEY, idempotency_key TEXT NOT NULL UNIQUE, request_hash TEXT NOT NULL,
		payment_id UUID, status TEXT NOT NULL, gateway TEXT, provider_ref TEXT, error TEXT,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now());
		ALTER TABLE orders ADD COLUMN IF NOT EXISTS gateway TEXT;
		ALTER TABLE orders ADD COLUMN IF NOT EXISTS provider_ref TEXT;
		ALTER TABLE orders ADD COLUMN IF NOT EXISTS error TEXT`)
	return err
}
