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

package service

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/Yukthi-Systems/Phoenix-Admin-Alerts/internal/models"
)

// ProcessMailbox records mailbox as seen for this run, computes its
// password expiry date from PasswordUpdatedAt plus the domain's
// MaxPasswordAge, and:
//
//   - queues an expiry-status update on s.expiryChan if the computed
//     state differs from the stored IsPasswordExpired flag;
//   - for each configured NotifyAt threshold, publishes a
//     "password_expiry_warning" alert exactly once when days-remaining
//     first reaches that threshold (de-duplicated via the quota
//     database), and clears the alert state once the password is
//     renewed and days-remaining rises back above it.
//
// It is a no-op if the domain does not enforce password expiry or the
// mailbox has never had its password set. It is safe to call
// concurrently across mailboxes; it is invoked from the worker pool
// started by Service.Start.
func (s *Service) ProcessMailbox(ctx context.Context, mailbox models.Mailbox, domain models.Domain) {
	// Track seen email for state cleanup
	s.mu.Lock()
	s.processedMailboxes = append(s.processedMailboxes, mailbox.Email)
	s.mu.Unlock()

	props := domain.MaxPasswordAgeProperties
	if !props.EnableMaxPasswordAge || props.MaxPasswordAge <= 0 {
		return
	}

	if mailbox.PasswordUpdatedAt.IsZero() {
		return
	}

	// Calculate expiry — normalize to UTC first to strip timezone offset
	expiryDate := mailbox.PasswordUpdatedAt.UTC().AddDate(0, 0, props.MaxPasswordAge)

	// Use time.Date() in UTC for a timezone-safe day-level comparison.
	// Truncate(24h) is NOT timezone-safe; it truncates from Go's zero-time in UTC
	// which can give wrong results when values carry a non-UTC location.
	nowUTC := time.Now().UTC()
	nowDay := time.Date(nowUTC.Year(), nowUTC.Month(), nowUTC.Day(), 0, 0, 0, 0, time.UTC)
	expiryDay := time.Date(expiryDate.Year(), expiryDate.Month(), expiryDate.Day(), 0, 0, 0, 0, time.UTC)

	daysRemaining := int(expiryDay.Sub(nowDay).Hours() / 24)
	slog.Debug("Mailbox processed", "email", mailbox.Email, "days_remaining", daysRemaining)

	// Update main DB status if it changed
	shouldBeExpired := daysRemaining <= 0
	if mailbox.IsPasswordExpired != shouldBeExpired {
		slog.Info("Password expiry status changed",
			"email", mailbox.Email,
			"expired", mailbox.IsPasswordExpired,
			"shouldBeExpired", shouldBeExpired,
			"days_remaining", daysRemaining)
		// Comment for the database update: We are not updating the main database directly here.
		// Instead, we send an update to the expiry channel for processing elsewhere in the system.
		// This design allows for decoupling the mailbox processing logic from the database update logic,
		//  enabling better scalability and maintainability of the codebase.
		s.expiryChan <- expiryUpdate{email: mailbox.Email, expired: shouldBeExpired}
	}

	// Threshold check
	sort.Ints(props.NotifyAt)
	for _, t := range props.NotifyAt {
		if daysRemaining == t {
			isActive, err := s.quotaDB.GetAlertState(ctx, "password_expiry", mailbox.Email, t)
			if err != nil {
				slog.Error("Failed to get alert state", "email", mailbox.Email, "error", err)
				continue
			}

			if !isActive {
				name := strings.TrimSpace(fmt.Sprintf("%s %s", mailbox.FirstName, mailbox.LastName))
				if name == "" {
					name = mailbox.Email
				}

				variables := map[string]any{
					"name":           name,
					"email":          mailbox.Email,
					"days_remaining": daysRemaining,
					"expiry_date":    expiryDate.Format("2006-01-02"),
					"last_updated":   mailbox.PasswordUpdatedAt.Format("2006-01-02"),
				}

				if err := s.publisher.PublishEmail(ctx, mailbox.Email, "password_expiry_warning", variables); err != nil {
					slog.Error("Failed to publish password expiry alert", "email", mailbox.Email, "error", err)
				} else {
					slog.Info("PASSWORD EXPIRY ALERT",
						"name", name,
						"email", mailbox.Email,
						"days_remaining", daysRemaining,
						"expiry_date", expiryDate.Format("2006-01-02"),
						"last_updated", mailbox.PasswordUpdatedAt.Format("2006-01-02"),
					)
					if err := s.quotaDB.SetAlertState(ctx, "password_expiry", mailbox.Email, t, true); err != nil {
						slog.Error("Failed to set alert state", "email", mailbox.Email, "error", err)
					}
				}
			}
		} else if daysRemaining > t {
			// Reset alert state if we are above the threshold (e.g. password was updated)
			isActive, err := s.quotaDB.GetAlertState(ctx, "password_expiry", mailbox.Email, t)
			if err == nil && isActive {
				if err := s.quotaDB.SetAlertState(ctx, "password_expiry", mailbox.Email, t, false); err != nil {
					slog.Error("Failed to reset alert state", "email", mailbox.Email, "error", err)
				}
			}
		}
	}
}
