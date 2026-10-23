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

func TestAllows(t *testing.T) {
	if !Allows([]string{"deploy"}, ScopeDeploy, "demo", "production") {
		t.Fatal("global deploy should allow any project")
	}
	if Allows([]string{"read"}, ScopeDeploy, "demo", "production") {
		t.Fatal("read should not allow deploy")
	}
	if !Allows([]string{"project:demo:deploy"}, ScopeDeploy, "demo", "production") {
		t.Fatal("project deploy should allow the project")
	}
	if Allows([]string{"project:demo:deploy"}, ScopeDeploy, "other", "production") {
		t.Fatal("project deploy leaked to another project")
	}
	if !Allows([]string{"project:demo/production:deploy"}, ScopeDeploy, "demo", "production") {
		t.Fatal("environment deploy should allow the target")
	}
	if Allows([]string{"project:demo/production:deploy"}, ScopeDeploy, "demo", "staging") {
		t.Fatal("environment deploy leaked to another environment")
	}
	if err := ValidateScopes([]string{"project:demo:read", "project:demo/staging:deploy"}); err != nil {
		t.Fatal(err)
	}
}

func TestRequiredCommandScope(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want Scope
	}{
		{[]string{"status"}, ScopeRead},
		{[]string{"app", "deploy", "api", "--project", "demo"}, ScopeDeploy},
		{[]string{"app", "status", "api"}, ScopeRead},
		{[]string{"secret", "set", "X"}, ScopeSecrets},
		{[]string{"registry", "credential"}, ScopeRegistry},
		{[]string{"apply"}, ScopeDeploy},
	} {
		got, _, _, ok := RequiredCommandScope(tc.args)
		if !ok || got != tc.want {
			t.Fatalf("%v: got=%s ok=%v want=%s", tc.args, got, ok, tc.want)
		}
	}
	_, project, environment, _ := RequiredCommandScope([]string{"app", "deploy", "api", "--project", "demo", "-e", "staging"})
	if project != "demo" || environment != "staging" {
		t.Fatalf("target = %s/%s", project, environment)
	}
}

func TestCheckCommand(t *testing.T) {
	reader := Profile{Name: "reader", Scopes: []string{"read"}}
	if err := CheckCommand(reader, []string{"status"}); err != nil {
		t.Fatal(err)
	}
	if err := CheckCommand(reader, []string{"app", "deploy", "api"}); err == nil {
		t.Fatal("reader allowed deploy")
	}
	deployer := Profile{Name: "deployer", Scopes: []string{"project:demo:deploy"}}
	if err := CheckCommand(deployer, []string{"app", "deploy", "api", "--project", "demo"}); err != nil {
		t.Fatal(err)
	}
	if err := CheckCommand(deployer, []string{"app", "deploy", "api", "--project", "other"}); err == nil {
		t.Fatal("project deploy leaked")
	}
}
