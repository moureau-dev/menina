package release

import "testing"

func TestStateTransitions(t *testing.T) {
	cases := []struct {
		from, to State
		ok       bool
	}{
		{Prepared, Deploying, true},
		{Prepared, Active, true},
		{Deploying, Verifying, true},
		{Verifying, Active, true},
		{Deploying, Failed, true},
		{Deploying, RolledBack, true},
		{Failed, Recovering, true},
		{Recovering, RolledBack, true},
		{RolledBack, Active, false},
		{Active, Active, true},
	}
	for _, tc := range cases {
		if got := CanTransition(tc.from, tc.to); got != tc.ok {
			t.Errorf("CanTransition(%s, %s) = %v, want %v", tc.from, tc.to, got, tc.ok)
		}
	}
}

func TestRecoveryPolicy(t *testing.T) {
	triggers := []RecoveryTrigger{TriggerDeployError, TriggerVerificationFailed}
	if !ShouldRecover(RecoveryPrevious, triggers, TriggerVerificationFailed) {
		t.Fatal("previous policy should recover from a verification failure")
	}
	if ShouldRecover(RecoveryNone, triggers, TriggerDeployError) {
		t.Fatal("none policy must never auto-recover")
	}
	if ShouldRecover(RecoveryRequireApproval, triggers, TriggerDeployError) {
		t.Fatal("require-approval must not recover automatically")
	}
	if ShouldRecover(RecoveryPrevious, []RecoveryTrigger{TriggerTimeout}, TriggerDeployError) {
		t.Fatal("unlisted trigger must not recover")
	}
}
