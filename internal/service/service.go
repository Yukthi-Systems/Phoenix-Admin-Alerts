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

// Package service implements the core alert-processing pipeline: it walks
// active organizations and their domains and mailboxes, evaluates quota
// and password-expiry thresholds, de-duplicates alerts against the quota
// database, and publishes notifications through RabbitMQ.
//
// A single run, triggered by Service.Start, processes organizations
// sequentially but fans mailbox work out to a fixed-size worker pool (see
// ProcessMailbox) since mailbox volume dominates the workload. Password
// expiry status changes are batched and flushed to the primary database
// by a dedicated collector goroutine (see startExpiryCollector) to avoid
// one write per mailbox.
package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/Yukthi-Systems/Phoenix-Admin-Alerts/internal/database"
	"github.com/Yukthi-Systems/Phoenix-Admin-Alerts/internal/models"
	"github.com/Yukthi-Systems/Phoenix-Admin-Alerts/internal/rabbitmq"
)

// mailboxWork is a unit of work queued for the mailbox worker pool: a
// mailbox plus the domain it belongs to (needed for password policy).
type mailboxWork struct {
	mailbox models.Mailbox
	domain  models.Domain
}

// expiryUpdate is a password-expiry status change queued for the batch
// collector to flush to the primary database.
type expiryUpdate struct {
	email   string
	expired bool
}

// AlertPublisher is the subset of rabbitmq.Publisher that Service depends
// on, allowing the RabbitMQ connection to be swapped out (e.g. in tests).
type AlertPublisher interface {
	PublishEmail(ctx context.Context, to string, templateName string, variables map[string]interface{}) error
	Close()
}

// Service orchestrates a single end-to-end alert processing run:
// fetching organizations/domains/mailboxes from the primary database,
// evaluating thresholds, publishing alerts, and recording alert state so
// each breach is only notified once.
type Service struct {
	db                     database.Service
	quotaDB                database.QuotaService
	processedMailboxes     []string
	processedDomains       []string
	processedOrganizations []string
	mu                     sync.Mutex
	mailboxChan            chan mailboxWork
	expiryChan             chan expiryUpdate
	quotaThresholds        []int
	publisher              AlertPublisher
}

// New constructs a Service backed by db, initializing the quota database
// connection, the RabbitMQ publisher (RABBITMQ_URL, RABBITMQ_QUEUE), and
// the quota alert thresholds (QUOTA_THRESHOLDS, a comma-separated list of
// percentages, sorted descending; defaults to 95,85,80 if unset or
// unparseable).
func New(db database.Service) (*Service, error) {
	qdb := database.NewQuotaService()

	// Initialize RabbitMQ publisher
	rmqURL := os.Getenv("RABBITMQ_URL")
	rmqQueue := os.Getenv("RABBITMQ_QUEUE")
	if rmqURL == "" {
		rmqURL = "amqp://guest:guest@localhost:5672/"
	}
	if rmqQueue == "" {
		rmqQueue = "alerts"
	}
	pub, err := rabbitmq.NewPublisher(rmqURL, rmqQueue)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize rabbitmq publisher: %w", err)
	}

	// Parse thresholds from environment
	thresholds := []int{95, 85, 80} // Default values
	if envThresholds := os.Getenv("QUOTA_THRESHOLDS"); envThresholds != "" {
		parts := strings.Split(envThresholds, ",")
		var parsed []int
		for _, p := range parts {
			if val, err := strconv.Atoi(strings.TrimSpace(p)); err == nil {
				parsed = append(parsed, val)
			}
		}
		if len(parsed) > 0 {
			thresholds = parsed
		}
	}

	// Ensure thresholds are sorted descending for highest-breached logic
	sort.Slice(thresholds, func(i, j int) bool {
		return thresholds[i] > thresholds[j]
	})

	return &Service{
		db:              db,
		quotaDB:         qdb,
		quotaThresholds: thresholds,
		publisher:       pub,
	}, nil
}

// Start runs one full processing pass: it (re)initializes the quota
// database schema, spins up the mailbox worker pool and expiry
// collector, streams every active organization through
// ProcessOrganization, then blocks until all queued work drains before
// pruning alert state for entities no longer seen. It is intended to be
// invoked once per cron tick or CLI --once run and is not safe to call
// concurrently with itself.
func (s *Service) Start() {
	ctx := context.Background()
	slog.Info("Starting Admin Alerts Service with Worker Pool")

	// Initialize Quota DB Schema
	if err := s.quotaDB.InitSchema(ctx); err != nil {
		slog.Error("Failed to initialize quota schema", "error", err)
	}

	// Reset processed tracking for this run
	s.processedMailboxes = []string{}
	s.processedDomains = []string{}
	s.processedOrganizations = []string{}

	// Initialize Worker Pool
	const numWorkers = 50
	s.mailboxChan = make(chan mailboxWork, 1000)
	s.expiryChan = make(chan expiryUpdate, 1000)
	var wg sync.WaitGroup
	var collectorWG sync.WaitGroup

	// Start Expiry Collector
	collectorWG.Add(1)
	go func() {
		defer collectorWG.Done()
		s.startExpiryCollector(ctx)
	}()

	// Start Workers
	for i := range numWorkers {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for work := range s.mailboxChan {
				s.ProcessMailbox(ctx, work.mailbox, work.domain)
			}
		}(i)
	}

	// Fetch and stream work
	orgs, err := s.db.GetOrganizations(ctx)
	if err != nil {
		slog.Error("Failed to fetch organizations", "error", err)
		close(s.mailboxChan)
		close(s.expiryChan)
		return
	}

	for _, org := range orgs {
		// Process each organization
		s.ProcessOrganization(ctx, org)
	}

	// Close channels and wait for workers to finish
	close(s.mailboxChan)
	wg.Wait()

	// Finish expiry collection
	close(s.expiryChan)
	collectorWG.Wait()

	// Cleanup state for entities that no longer exist
	if err := s.quotaDB.CleanupOldAlerts(ctx, "mailbox", s.processedMailboxes); err != nil {
		slog.Error("Failed to cleanup mailbox alerts", "error", err)
	}
	if err := s.quotaDB.CleanupOldAlerts(ctx, "domain", s.processedDomains); err != nil {
		slog.Error("Failed to cleanup domain alerts", "error", err)
	}
	if err := s.quotaDB.CleanupOldAlerts(ctx, "organization", s.processedOrganizations); err != nil {
		slog.Error("Failed to cleanup organization alerts", "error", err)
	}

	slog.Info("Service processing complete")
}

// HealthCheck reports the primary database's connectivity status.
func (s *Service) HealthCheck() map[string]string {
	return s.db.Health()
}

// Close releases the RabbitMQ publisher and quota database connection.
// It should be called once, on application shutdown.
func (s *Service) Close() {
	if s.publisher != nil {
		s.publisher.Close()
	}
	if s.quotaDB != nil {
		slog.Info("closing quota database connection pool")
		s.quotaDB.Close()
	}
}

// startExpiryCollector drains s.expiryChan, batching password expiry
// status changes into groups of batchSize before flushing each batch to
// the primary database via UpdatePasswordExpiry. It runs until
// s.expiryChan is closed, flushing any remainder before returning.
func (s *Service) startExpiryCollector(ctx context.Context) {
	const batchSize = 500
	toExpire := make([]string, 0, batchSize)
	toActivate := make([]string, 0, batchSize)

	flush := func(expired bool) {
		var emails []string
		if expired {
			emails = toExpire
		} else {
			emails = toActivate
		}

		if len(emails) == 0 {
			return
		}

		if err := s.db.UpdatePasswordExpiry(ctx, emails, expired); err != nil {
			slog.Error("Failed to bulk update password expiry", "expired", expired, "count", len(emails), "error", err)
		} else {
			slog.Info("Bulk updated password expiry", "expired", expired, "count", len(emails))
		}

		if expired {
			toExpire = make([]string, 0, batchSize)
		} else {
			toActivate = make([]string, 0, batchSize)
		}
	}

	for update := range s.expiryChan {
		if update.expired {
			toExpire = append(toExpire, update.email)
			if len(toExpire) >= batchSize {
				flush(true)
			}
		} else {
			toActivate = append(toActivate, update.email)
			if len(toActivate) >= batchSize {
				flush(false)
			}
		}
	}
	flush(true)
	flush(false)
}
