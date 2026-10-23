package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// DefaultEnrollmentTTL is the default lifetime of an enrollment token.
const DefaultEnrollmentTTL = 15 * time.Minute

// MaxEnrollmentTTL bounds enrollment token lifetime.
const MaxEnrollmentTTL = 24 * time.Hour

// ErrEnrollmentInvalid is returned for unknown, expired, or already-used
// enrollment tokens. Callers should not distinguish these cases to avoid
// leaking token state.
var ErrEnrollmentInvalid = errors.New("enrollment token is invalid or expired")

// EnrollmentToken is the stored half of an enrollment token. The plaintext is
// returned once at creation and never stored.
type EnrollmentToken struct {
	ID        string    `json:"id"`
	ProfileID string    `json:"profileId"`
	TokenHash string    `json:"-"`
	ExpiresAt time.Time `json:"expiresAt"`
	CreatedAt time.Time `json:"createdAt"`
	UsedAt    time.Time `json:"usedAt,omitempty"`
}

// NewEnrollmentToken generates a one-time token and its stored representation.
func NewEnrollmentToken(profileID string, ttl time.Duration) (string, EnrollmentToken, error) {
	if profileID == "" {
		return "", EnrollmentToken{}, fmt.Errorf("profile id is required")
	}
	if ttl <= 0 {
		ttl = DefaultEnrollmentTTL
	}
	if ttl > MaxEnrollmentTTL {
		return "", EnrollmentToken{}, fmt.Errorf("enrollment ttl must be at most %s", MaxEnrollmentTTL)
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", EnrollmentToken{}, err
	}
	plaintext := "menina_enroll_" + base64.RawURLEncoding.EncodeToString(buf)
	return plaintext, EnrollmentToken{
		ProfileID: profileID,
		TokenHash: HashEnrollmentToken(plaintext),
		ExpiresAt: time.Now().UTC().Add(ttl),
	}, nil
}

// HashEnrollmentToken returns the stable hash stored at rest.
func HashEnrollmentToken(plaintext string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(plaintext)))
	return hex.EncodeToString(sum[:])
}

// Expired reports whether the token has passed its expiry at now.
func (t EnrollmentToken) Expired(now time.Time) bool {
	return !now.Before(t.ExpiresAt)
}

// Used reports whether the token has already been consumed.
func (t EnrollmentToken) Used() bool { return !t.UsedAt.IsZero() }
