package identity

import (
	"testing"
	"time"
)

func TestValidateScopes(t *testing.T) {
	if err := ValidateScopes([]string{"deploy", "read"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateScopes([]string{"deploy", "deploy"}); err == nil {
		t.Fatal("duplicate scope accepted")
	}
	if err := ValidateScopes([]string{"root"}); err == nil {
		t.Fatal("unknown scope accepted")
	}
}

func TestProfileAndKeyValidation(t *testing.T) {
	if err := (Profile{Status: StatusActive}).Validate(); err == nil {
		t.Fatal("profile without name accepted")
	}
	if err := (Profile{Name: "ops", Status: StatusActive}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Key{ProfileID: "p", Fingerprint: "SHA256:x", Status: StatusActive}).Validate(); err != nil {
		t.Fatal(err)
	}
	expired := Key{ExpiresAt: time.Now().Add(-time.Minute)}
	if !expired.Expired(time.Now()) {
		t.Fatal("expired key reported valid")
	}
}

func TestEnrollmentToken(t *testing.T) {
	plaintext, token, err := NewEnrollmentToken("profile", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if plaintext == "" || token.TokenHash == plaintext || token.TokenHash != HashEnrollmentToken(plaintext) {
		t.Fatalf("token=%+v plaintext=%q", token, plaintext)
	}
	if token.Expired(time.Now().Add(2*time.Minute)) != true {
		t.Fatal("token should be expired")
	}
	if _, _, err := NewEnrollmentToken("", time.Minute); err == nil {
		t.Fatal("empty profile accepted")
	}
	if _, _, err := NewEnrollmentToken("profile", MaxEnrollmentTTL+time.Second); err == nil {
		t.Fatal("oversized ttl accepted")
	}
}
