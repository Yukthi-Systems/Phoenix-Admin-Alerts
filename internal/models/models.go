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

// Package models defines the data types shared between the database and
// service layers: organizations, domains, and mailboxes as read from the
// primary mail database, plus their nested policy/contact structures.
package models

import (
	"time"

	"github.com/google/uuid"
)

// Organization is a tenant in the primary mail database, including its
// storage quota, email identity counts, and (optional) chat quota usage.
type Organization struct {
	OrganizationID           uuid.UUID `json:"organization_id"`
	OrganizationName         string    `json:"organization_name"`
	OrganizationInfo         OrgInfo   `json:"organization_info"`
	IsActive                 bool      `json:"is_active"`
	QuotaAllocated           float64   `json:"quota_allocated"`
	QuotaUtilized            float64   `json:"quota_utilized"`
	ChatServiceEnabled       bool      `json:"chat_service_enabled"`
	EmailServiceEnabled      bool      `json:"email_service_enabled"`
	AllocatedEmailIdentities int64     `json:"allocated_email_identities"`
	UtilizedEmailIdentities  int64     `json:"utilized_email_identities"`
	ChatQuotaAllocated       float64   `json:"chat_quota_allocated"`
	ChatQuotaUtilized        float64   `json:"chat_quota_utilized"`
}

// OrgInfo holds free-form organization metadata, currently just the set
// of contacts to notify on quota alerts, keyed by an implementation
// defined contact identifier.
type OrgInfo struct {
	ContactInfo map[string]Contact `json:"contact_info"`
}

// Contact is a single notification recipient for an organization.
type Contact struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Phone string `json:"phone"`
}

// MaxPasswordAgeProperties is a domain's password-expiry policy: whether
// expiry is enforced, the maximum password age in days, and the list of
// "days remaining" thresholds at which a warning alert should fire.
type MaxPasswordAgeProperties struct {
	NotifyAt             []int `json:"notify_at"`
	MaxPasswordAge       int   `json:"max_password_age"`
	EnableMaxPasswordAge bool  `json:"enable_max_password_age"`
}

// Domain is a mail domain managed by an Organization, carrying its
// password-expiry policy.
type Domain struct {
	DomainName               string                   `json:"domain_name"`
	ManagedBy                uuid.UUID                `json:"managed_by"`
	IsActive                 bool                     `json:"is_active"`
	MaxPasswordAge           int                      `json:"max_password_age"`
	MaxPasswordAgeProperties MaxPasswordAgeProperties `json:"max_password_age_properties"`
}

// Mailbox is a single email identity belonging to a Domain, including its
// per-mailbox storage quota and password expiry state.
type Mailbox struct {
	Email              string    `json:"email"`
	IsEnabled          bool      `json:"is_enabled"`
	DomainName         string    `json:"domain_name"`
	FirstName          string    `json:"first_name"`
	LastName           string    `json:"last_name"`
	PrimaryPhone       string    `json:"primary_phone"`
	SecondaryEmail     string    `json:"secondary_email"`
	IsPasswordExpired  bool      `json:"is_password_expired"`
	PasswordUpdatedAt  time.Time `json:"password_updated_at"`
	QuotaAllocated     float64   `json:"quota_allocated"`
	QuotaUtilizedBytes int64     `json:"quota_utilized_bytes"`
}
