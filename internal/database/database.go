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

// Package database provides connection-pooled access to the primary mail
// service PostgreSQL database. It exposes typed queries for organizations,
// domains, and mailboxes that the service package uses to drive alert
// processing.
//
// See the sibling file quota_db.go for the separate quota/alert-state
// database used to de-duplicate notifications; the two are intentionally
// distinct connection pools against distinct databases.
package database

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/Yukthi-Systems/Phoenix-Admin-Alerts/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Service is the primary mail database access layer used by the alert
// processing pipeline. Implementations must be safe for concurrent use.
type Service interface {
	// Health reports connectivity status, keyed by "status" ("up"/"down")
	// with an optional "message" or "error" detail.
	Health() map[string]string

	// Close releases the underlying connection pool. It is safe to call
	// during shutdown and does not return an error from pgxpool itself.
	Close() error

	// GetPool returns the underlying pgx connection pool for callers that
	// need direct access outside the typed query methods below.
	GetPool() *pgxpool.Pool

	// Queries

	// GetOrganizations returns all active organizations, including their
	// email and (optional) chat quota allocation/utilization figures.
	GetOrganizations(ctx context.Context) ([]models.Organization, error)

	// GetDomainsByOrgID returns the active domains managed by the given
	// organization, including their password-expiry policy.
	GetDomainsByOrgID(ctx context.Context, orgID uuid.UUID) ([]models.Domain, error)

	// GetMailboxesByDomain returns the enabled mailboxes belonging to the
	// given domain.
	GetMailboxesByDomain(ctx context.Context, domainName string) ([]models.Mailbox, error)

	// UpdatePasswordExpiry bulk-flags the given email addresses as
	// expired or not expired. It is a no-op when emails is empty.
	UpdatePasswordExpiry(ctx context.Context, emails []string, expired bool) error
}

// service is the default Service implementation, backed by a pgx
// connection pool to the primary mail database.
type service struct {
	db *pgxpool.Pool
}

var (
	dbInstance *service
	once       sync.Once
)

// New returns the process-wide Service singleton, establishing the
// connection pool to the primary mail database (configured via the
// DB_HOST, DB_PORT, DB_DATABASE, DB_USERNAME, and DB_PASSWORD environment
// variables) on first call. Subsequent calls return the same instance.
//
// New terminates the process via os.Exit if the connection configuration
// is invalid or the pool cannot be created, since the service cannot
// operate without it.
func New() Service {
	once.Do(func() {
		database := os.Getenv("DB_DATABASE")
		password := os.Getenv("DB_PASSWORD")
		username := os.Getenv("DB_USERNAME")
		port := os.Getenv("DB_PORT")
		host := os.Getenv("DB_HOST")
		sslmode := os.Getenv("DB_SSLMODE")

		connStr := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s", username, password, host, port, database, sslmode)
		// Masking password for logs
		slog.Info("connecting to database", "host", host, "port", port, "database", database)

		config, err := pgxpool.ParseConfig(connStr)
		if err != nil {
			slog.Error("unable to parse database config", "error", err)
			os.Exit(1)
		}

		pool, err := pgxpool.NewWithConfig(context.Background(), config)
		if err != nil {
			slog.Error("unable to create connection pool", "error", err)
			os.Exit(1)
		}

		dbInstance = &service{
			db: pool,
		}
	})
	return dbInstance
}

// Health pings the primary database and reports its status.
func (s *service) Health() map[string]string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stats := make(map[string]string)

	err := s.db.Ping(ctx)
	if err != nil {
		stats["status"] = "down"
		stats["error"] = fmt.Sprintf("main database down: %v", err)
		slog.Error("main database health check failed", "error", err)
		return stats
	}

	stats["status"] = "up"
	stats["message"] = "It's healthy"
	return stats
}

// Close shuts down the connection pool.
func (s *service) Close() error {
	slog.Info("closing database connection pool")
	s.db.Close()
	return nil
}

// GetPool returns the underlying pgx connection pool.
func (s *service) GetPool() *pgxpool.Pool {
	return s.db
}

// GetOrganizations returns every active organization, left-joining chat
// quota settings so ChatQuotaAllocated/ChatQuotaUtilized default to zero
// for organizations without chat enabled.
func (s *service) GetOrganizations(ctx context.Context) ([]models.Organization, error) {
	rows, err := s.db.Query(
		ctx,
		`SELECT
			ORG.ORGANIZATION_ID,
			ORG.CHAT_SERVICE_ENABLED,
			ORG.EMAIL_SERVICE_ENABLED,
			ORG.ORGANIZATION_NAME,
			ORG.ORGANIZATION_INFO,
			ORG.QUOTA_ALLOCATED,
			ORG.QUOTA_UTILIZED,
			ORG.ALLOCATED_EMAIL_IDENTITIES,
			ORG.UTILIZED_EMAIL_IDENTITIES,
			ORG.IS_ACTIVE,
			COALESCE(CS.QUOTA_ALLOCATED, 0) AS CHAT_QUOTA_ALLOCATED,
			COALESCE(CS.QUOTA_UTILIZED, 0) AS CHAT_QUOTA_UTILIZED
		FROM
			ORGANIZATIONS AS ORG
			LEFT JOIN CHAT_SETTINGS AS CS ON ORG.ORGANIZATION_ID = CS.ORGANIZATION_ID
		WHERE
			ORG.IS_ACTIVE = TRUE`,
	)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByName[models.Organization])
}

// GetDomainsByOrgID returns the active domains managed by orgID.
func (s *service) GetDomainsByOrgID(ctx context.Context, orgID uuid.UUID) ([]models.Domain, error) {
	rows, err := s.db.Query(ctx,
		`SELECT 
			domain_name, 
			managed_by, 
			is_active, 
			max_password_age, 
			max_password_age_properties 
		FROM domains 
		WHERE managed_by = $1 
		AND is_active = true`,
		orgID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByName[models.Domain])
}

// GetMailboxesByDomain returns the enabled mailboxes on domainName.
func (s *service) GetMailboxesByDomain(ctx context.Context, domainName string) ([]models.Mailbox, error) {
	rows, err := s.db.Query(ctx,
		`SELECT
			ei.email,
			mb.is_enabled,
			ei.domain_name,
			ei.first_name,
			COALESCE(ei.last_name, '') as last_name,
			COALESCE(ei.primary_phone, '') as primary_phone,
			COALESCE(ei.secondary_email, '') as secondary_email,
			ei.is_password_expired,
			ei.password_updated_at,
			mb.quota_allocated,
			mb.quota_utilized_bytes
		FROM email_identities ei
		INNER JOIN mailboxes mb
			ON ei.email = mb.email
		AND ei.domain_name = mb.domain_name
		WHERE  ei.is_enabled = true 
		AND mb.is_enabled = true
		AND ei.domain_name = $1`,
		domainName)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByName[models.Mailbox])
}

// UpdatePasswordExpiry bulk-sets the is_password_expired flag for emails.
// It is a no-op when emails is empty.
func (s *service) UpdatePasswordExpiry(ctx context.Context, emails []string, expired bool) error {
	if len(emails) == 0 {
		return nil
	}
	query := `UPDATE email_identities SET is_password_expired = $1 WHERE email = ANY($2)`
	_, err := s.db.Exec(ctx, query, expired, emails)
	return err
}
