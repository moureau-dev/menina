// Package identity defines server-derived client profiles and their keys.
package identity

import (
	"fmt"
	"time"
)

// Status is the lifecycle state of a profile or key.
type Status string

const (
	StatusActive   Status = "active"
	StatusDisabled Status = "disabled"
	StatusRevoked  Status = "revoked"
)

// Scope is a permission granted to a profile. Enforcement lands with the
// authorization work; the model stores scopes now.
type Scope string

const (
	ScopeAdmin    Scope = "admin"
	ScopeDeploy   Scope = "deploy"
	ScopeRead     Scope = "read"
	ScopeSecrets  Scope = "secrets"
	ScopeRegistry Scope = "registry"
)

// AllScopes is the supported scope set.
var AllScopes = []Scope{ScopeAdmin, ScopeDeploy, ScopeRead, ScopeSecrets, ScopeRegistry}

// Profile is a server-derived client identity.
type Profile struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Scopes     []string  `json:"scopes"`
	Status     Status    `json:"status"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
	LastSeenAt time.Time `json:"lastSeenAt,omitempty"`
}

// Key is an authenticated key attached to a profile.
type Key struct {
	ID          string    `json:"id"`
	ProfileID   string    `json:"profileId"`
	Fingerprint string    `json:"fingerprint"`
	PublicKey   string    `json:"publicKey,omitempty"`
	Comment     string    `json:"comment,omitempty"`
	Status      Status    `json:"status"`
	ExpiresAt   time.Time `json:"expiresAt,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	LastSeenAt  time.Time `json:"lastSeenAt,omitempty"`
}

// Expired reports whether the key is past its expiry.
func (k Key) Expired(now time.Time) bool {
	return !k.ExpiresAt.IsZero() && !now.Before(k.ExpiresAt)
}

// Validate checks profile fields.
func (p Profile) Validate() error {
	if p.Name == "" {
		return fmt.Errorf("profile name is required")
	}
	if err := ValidateStatus(p.Status); err != nil {
		return err
	}
	return ValidateScopes(p.Scopes)
}

// ValidateScopes checks that every scope is supported and there are no
// duplicates.
func ValidateScopes(scopes []string) error {
	seen := map[string]bool{}
	for _, scope := range scopes {
		if seen[scope] {
			return fmt.Errorf("duplicate scope %q", scope)
		}
		seen[scope] = true
		if err := ValidateScope(scope); err != nil {
			return err
		}
	}
	return nil
}

// ValidateStatus checks a profile or key status.
func ValidateStatus(status Status) error {
	switch status {
	case StatusActive, StatusDisabled, StatusRevoked:
		return nil
	case "":
		return fmt.Errorf("status is required")
	default:
		return fmt.Errorf("unknown status %q", status)
	}
}

// Validate checks key fields.
func (k Key) Validate() error {
	if k.ProfileID == "" {
		return fmt.Errorf("profile id is required")
	}
	if k.Fingerprint == "" {
		return fmt.Errorf("key fingerprint is required")
	}
	return ValidateStatus(k.Status)
}
