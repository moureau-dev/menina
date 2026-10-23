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
