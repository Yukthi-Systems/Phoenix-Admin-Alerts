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

	"github.com/Yukthi-Systems/Phoenix-Admin-Alerts/internal/models"
)

// ProcessOrganization records org as seen for this run and evaluates its
// storage, email-identity, and (if enabled) chat quota usage against
// s.quotaThresholds. If the highest breached threshold does not already
// have an active alert, it publishes an "org_quota_warning" (or
// "org_quota_warning_chat" when chat is enabled) alert to every contact
// on the organization and records the alert as active; thresholds no
// longer breached have their alert state silently reset. Finally it
// fetches org's domains and processes each via ProcessDomain.
func (s *Service) ProcessOrganization(ctx context.Context, org models.Organization) {
	// Track seen organization for state cleanup
	s.mu.Lock()
	s.processedOrganizations = append(s.processedOrganizations, org.OrganizationID.String())
	s.mu.Unlock()

	thresholds := s.quotaThresholds
	var highestBreached int = -1

	// Calculate Storage/Quota usage
	var storageUsage float64
	if org.QuotaAllocated > 0 {
		storageUsage = (org.QuotaUtilized / org.QuotaAllocated) * 100
		for _, t := range thresholds {
			if storageUsage >= float64(t) {
				if t > highestBreached {
					highestBreached = t
				}
				break
			}
		}
	}

	// Calculate Email Identities usage
	var identityUsage float64
	if org.AllocatedEmailIdentities > 0 {
		identityUsage = (float64(org.UtilizedEmailIdentities) / float64(org.AllocatedEmailIdentities)) * 100
		for _, t := range thresholds {
			if identityUsage >= float64(t) {
				if t > highestBreached {
					highestBreached = t
				}
				break
			}
		}
	}

	// Calculate Chat Quota usage
	var chatUsage float64
	if org.ChatServiceEnabled && org.ChatQuotaAllocated > 0 {
		chatUsage = (org.ChatQuotaUtilized / org.ChatQuotaAllocated) * 100
		for _, t := range thresholds {
			if chatUsage >= float64(t) {
				if t > highestBreached {
					highestBreached = t
				}
				break
			}
		}
	}

	// If we breached a threshold, alert if not already sent
	if highestBreached != -1 {
		isActive, err := s.quotaDB.GetAlertState(ctx, "organization", org.OrganizationID.String(), highestBreached)
		if err != nil {
			slog.Error("Failed to get alert state", "org", org.OrganizationName, "error", err)
		} else if !isActive {
			slog.Info("QUOTA ALERT",
				"type", "organization",
				"name", org.OrganizationName,
				"id", org.OrganizationID,
				"threshold", highestBreached,
			)

			// Determine template and template variables
			templateName := "org_quota_warning"
			if org.ChatServiceEnabled {
				templateName = "org_quota_warning_chat"
			}

			variables := map[string]interface{}{
				"name":                           org.OrganizationName,
				"threshold":                      fmt.Sprintf("%d", highestBreached),
				"allocated_quota":                fmt.Sprintf("%.2f", org.QuotaAllocated),
				"utilized_quota":                 fmt.Sprintf("%.2f", org.QuotaUtilized),
				"storage_usage_percent":          fmt.Sprintf("%.2f", storageUsage),
				"allocated_email_identities":     org.AllocatedEmailIdentities,
				"utilized_email_identities":      org.UtilizedEmailIdentities,
				"email_identities_usage_percent": fmt.Sprintf("%.2f", identityUsage),
				"email_service_enabled":          org.EmailServiceEnabled,
				"chat_service_enabled":           org.ChatServiceEnabled,
			}

			if org.ChatServiceEnabled {
				variables["chat_quota_allocated"] = fmt.Sprintf("%.2f", org.ChatQuotaAllocated)
				variables["chat_quota_utilized"] = fmt.Sprintf("%.2f", org.ChatQuotaUtilized)
				variables["chat_quota_usage_percent"] = fmt.Sprintf("%.2f", chatUsage)
			}

			// Send alert to every contact in the org
			alertSent := false
			for _, contact := range org.OrganizationInfo.ContactInfo {
				if contact.Email == "" {
					slog.Warn("Contact email is empty, skipping", "org", org.OrganizationName, "contact", contact)
					continue
				}
				if err := s.publisher.PublishEmail(ctx, contact.Email, templateName, variables); err != nil {
					slog.Error("Failed to publish quota alert",
						"org", org.OrganizationName,
						"to", contact.Email,
						"error", err,
					)
				} else {
					slog.Info("Quota alert published",
						"org", org.OrganizationName,
						"to", contact.Email,
						"threshold", highestBreached,
					)
					alertSent = true
				}
			}

			if !alertSent {
				slog.Warn("No contacts found for org quota alert", "org", org.OrganizationName)
			}

			if err := s.quotaDB.SetAlertState(ctx, "organization", org.OrganizationID.String(), highestBreached, true); err != nil {
				slog.Error("Failed to set alert state", "org", org.OrganizationName, "error", err)
			}
		}
	}

	// Silent Reset
	for _, t := range thresholds {
		breached := false
		if org.QuotaAllocated > 0 && storageUsage >= float64(t) {
			breached = true
		}
		if org.AllocatedEmailIdentities > 0 && identityUsage >= float64(t) {
			breached = true
		}
		if org.ChatServiceEnabled && org.ChatQuotaAllocated > 0 && chatUsage >= float64(t) {
			breached = true
		}

		if !breached {
			isActive, err := s.quotaDB.GetAlertState(ctx, "organization", org.OrganizationID.String(), t)
			if err == nil && isActive {
				if err := s.quotaDB.SetAlertState(ctx, "organization", org.OrganizationID.String(), t, false); err != nil {
					slog.Error("Failed to reset alert state", "org", org.OrganizationName, "error", err)
				}
			}
		}
	}

	// Fetch and process domains identities password policies
	domains, err := s.db.GetDomainsByOrgID(ctx, org.OrganizationID)
	if err != nil {
		slog.Error("Failed to fetch domains", "org", org.OrganizationName, "error", err)
		return
	}
	for _, domain := range domains {
		s.ProcessDomain(ctx, domain)
	}
	slog.Debug("Processed organization", "org", org.OrganizationName)
}
