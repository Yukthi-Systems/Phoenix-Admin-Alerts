// Copyright (C) 2026 Yukthi Systems Private Limited
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License version 3
// as published by the Free Software Foundation.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// version 3 along with this program. If not, see
// <https://www.gnu.org/licenses/>.

package database

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// QuotaService tracks which alert thresholds have already been notified
// for a given entity, so the alert pipeline can send each breach exactly
// once and silently reset it once the entity falls back below threshold.
// Implementations must be safe for concurrent use.
type QuotaService interface {
	// Health reports connectivity status, keyed by "status" ("up"/"down")
	// with an optional "message" or "error" detail.
	Health() map[string]string

	// Close releases the underlying connection pool.
	Close() error

	// InitSchema creates the admin_alerts table if it does not already
	// exist. It is safe to call on every startup.
	InitSchema(ctx context.Context) error

	// Alert State Management

	// GetAlertState reports whether an alert is currently active
	// (already sent and not yet reset) for the given entity/threshold
	// pair. A missing row is treated as inactive, not an error.
	GetAlertState(ctx context.Context, entityType, entityID string, threshold int) (bool, error)

	// SetAlertState upserts the active flag for an entity/threshold
	// pair, e.g. true right after sending an alert, false once the
	// underlying condition (quota usage, password expiry) clears.
	SetAlertState(ctx context.Context, entityType, entityID string, threshold int, isActive bool) error

	// CleanupOldAlerts deletes alert-state rows for entityType whose
	// entity ID is not in currentIDs, so alerts for deleted or
	// deactivated entities don't linger forever. If currentIDs is
	// empty, all rows for entityType are deleted.
	CleanupOldAlerts(ctx context.Context, entityType string, currentIDs []string) error
}

// quotaService is the default QuotaService implementation, backed by a
// pgx connection pool to the quota/alert-state database.
type quotaService struct {
	db *pgxpool.Pool
}

// NewQuotaService creates a new QuotaService, establishing the connection
// pool to the quota database (configured via the QUOTA_DB_HOST,
// QUOTA_DB_PORT, QUOTA_DB_NAME, QUOTA_DB_USER, and QUOTA_DB_PASSWORD
// environment variables).
//
// NewQuotaService terminates the process via os.Exit if the connection
// configuration is invalid or the pool cannot be created.
func NewQuotaService() QuotaService {
	database := os.Getenv("QUOTA_DB_NAME")
	password := os.Getenv("QUOTA_DB_PASSWORD")
	username := os.Getenv("QUOTA_DB_USER")
	port := os.Getenv("QUOTA_DB_PORT")
	host := os.Getenv("QUOTA_DB_HOST")

	connStr := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", username, password, host, port, database)
	slog.Info("connecting to quota database", "host", host, "port", port, "database", database)

	config, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		slog.Error("unable to parse quota database config", "error", err)
		os.Exit(1)
	}

	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		slog.Error("unable to create quota connection pool", "error", err)
		os.Exit(1)
	}

	return &quotaService{
		db: pool,
	}
}

// Health pings the quota database and reports its status.
func (s *quotaService) Health() map[string]string {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	stats := make(map[string]string)

	err := s.db.Ping(ctx)
	if err != nil {
		stats["status"] = "down"
		stats["error"] = fmt.Sprintf("quota db down: %v", err)
		return stats
	}

	stats["status"] = "up"
	stats["message"] = "Quota DB is healthy"
	return stats
}

// Close shuts down the connection pool.
func (s *quotaService) Close() error {
	slog.Info("closing quota database connection pool")
	s.db.Close()
	return nil
}

// InitSchema creates the admin_alerts table if it does not already exist.
func (s *quotaService) InitSchema(ctx context.Context) error {
	query := `
	CREATE TABLE IF NOT EXISTS admin_alerts (
		id SERIAL PRIMARY KEY,
		entity_type TEXT NOT NULL,
		entity_id TEXT NOT NULL,
		threshold INTEGER NOT NULL,
		is_active BOOLEAN DEFAULT TRUE,
		updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(entity_type, entity_id, threshold)
	);`

	_, err := s.db.Exec(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to create admin_alerts table: %w", err)
	}
	slog.Info("admin_alerts table verified/created")
	return nil
}

// GetAlertState reports whether an alert is active for the given
// entity/threshold pair. A missing row reports (false, nil).
func (s *quotaService) GetAlertState(ctx context.Context, entityType, entityID string, threshold int) (bool, error) {
	var isActive bool
	query := `SELECT is_active FROM admin_alerts WHERE entity_type = $1 AND entity_id = $2 AND threshold = $3`
	err := s.db.QueryRow(ctx, query, entityType, entityID, threshold).Scan(&isActive)
	if err != nil {
		if err == pgx.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return isActive, nil
}

// SetAlertState upserts the active flag for an entity/threshold pair.
func (s *quotaService) SetAlertState(ctx context.Context, entityType, entityID string, threshold int, isActive bool) error {
	query := `
	INSERT INTO admin_alerts (entity_type, entity_id, threshold, is_active, updated_at)
	VALUES ($1, $2, $3, $4, CURRENT_TIMESTAMP)
	ON CONFLICT (entity_type, entity_id, threshold)
	DO UPDATE SET is_active = EXCLUDED.is_active, updated_at = CURRENT_TIMESTAMP`

	_, err := s.db.Exec(ctx, query, entityType, entityID, threshold, isActive)
	return err
}

// CleanupOldAlerts deletes admin_alerts rows for entityType whose entity
// ID is not present in currentIDs. Callers are expected to pass every
// entity ID seen during the current processing run, so this only prunes
// state for entities that no longer exist or were deactivated; an empty
// currentIDs deletes all rows for entityType.
func (s *quotaService) CleanupOldAlerts(ctx context.Context, entityType string, currentIDs []string) error {
	if len(currentIDs) == 0 {
		_, err := s.db.Exec(ctx, "DELETE FROM admin_alerts WHERE entity_type = $1", entityType)
		return err
	}

	query := `DELETE FROM admin_alerts WHERE entity_type = $1 AND entity_id != ALL($2)`
	_, err := s.db.Exec(ctx, query, entityType, currentIDs)
	return err
}
