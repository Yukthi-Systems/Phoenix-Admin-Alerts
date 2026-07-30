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
	"log/slog"

	"github.com/Yukthi-Systems/Phoenix-Admin-Alerts/internal/models"
)

// ProcessDomain records domain as seen for this run, then, if the domain
// enforces password expiry, fetches its mailboxes and enqueues each onto
// the mailbox worker pool for password-expiry evaluation
// (see ProcessMailbox). It is safe to call concurrently.
func (s *Service) ProcessDomain(ctx context.Context, domain models.Domain) {
	// Track seen domain for state cleanup
	s.mu.Lock()
	s.processedDomains = append(s.processedDomains, domain.DomainName)
	s.mu.Unlock()

	props := domain.MaxPasswordAgeProperties
	if !props.EnableMaxPasswordAge || props.MaxPasswordAge <= 0 {
		slog.Debug("Password expiry disabled for domain", "domain", domain.DomainName)
		return
	}

	// Fetch and process mailboxes
	mailboxes, err := s.db.GetMailboxesByDomain(ctx, domain.DomainName)
	if err != nil {
		slog.Error("Failed to fetch mailboxes", "domain", domain.DomainName, "error", err)
		return
	}

	for _, mailbox := range mailboxes {
		s.mailboxChan <- mailboxWork{mailbox: mailbox, domain: domain}
	}
	slog.Debug("Processed domain", "domain", domain.DomainName)
}
