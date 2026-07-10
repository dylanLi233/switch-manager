// Package postgres implements durable repositories with pgx.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DBTX is the subset shared by pgxpool.Pool and pgx.Tx.
type DBTX interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Store owns a PostgreSQL pool and repository constructors.
type Store struct {
	pool *pgxpool.Pool
}

// Open creates and verifies a PostgreSQL connection pool.
func Open(ctx context.Context, dsn string) (*Store, error) {
	if dsn == "" {
		return nil, errors.New("database DSN is required")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, mapDatabaseError(err, "", "")
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, mapDatabaseError(err, "", "")
	}
	return &Store{pool: pool}, nil
}

// NewStore wraps an already configured pool.
func NewStore(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("PostgreSQL pool is required")
	}
	return &Store{pool: pool}, nil
}

// Ping verifies database availability.
func (s *Store) Ping(ctx context.Context) error {
	if s == nil || s.pool == nil {
		return errors.New("PostgreSQL store is not initialized")
	}
	return mapDatabaseError(s.pool.Ping(ctx), "", "")
}

// Close releases pool resources.
func (s *Store) Close() {
	if s != nil && s.pool != nil {
		s.pool.Close()
	}
}

// Repositories contains repositories sharing one DBTX implementation.
type Repositories struct {
	Access               *AccessRepository
	Devices              *DeviceRepository
	Credentials          *CredentialRepository
	ExecutionCredentials *CredentialExecutionRepository
	Tasks                *TaskRepository
	Audits               *AuditRepository
}

func repositories(q DBTX) *Repositories {
	return &Repositories{
		Access:               &AccessRepository{q: q},
		Devices:              &DeviceRepository{q: q},
		Credentials:          &CredentialRepository{q: q},
		ExecutionCredentials: &CredentialExecutionRepository{q: q},
		Tasks:                &TaskRepository{q: q},
		Audits:               &AuditRepository{q: q},
	}
}

// Repositories returns pool-backed repositories.
func (s *Store) Repositories() *Repositories {
	return repositories(s.pool)
}

// WithinTx runs fn with repositories bound to a single transaction.
func (s *Store) WithinTx(ctx context.Context, fn func(*Repositories) error) error {
	if fn == nil {
		return errors.New("transaction function is required")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return mapDatabaseError(err, "", "")
	}
	if err := fn(repositories(tx)); err != nil {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			return errors.Join(err, fmt.Errorf("rollback transaction: %w", rollbackErr))
		}
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return mapDatabaseError(err, "", "")
	}
	return nil
}
