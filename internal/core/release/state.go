// Package release defines the per-app release lifecycle and its explicit state
// machine. It is portable and has no host dependencies.
package release

// State is an explicit app release state.
type State string

const (
	// Prepared means the release record exists and its target image and
	// configuration are pinned, but no workload change has started.
	Prepared State = "prepared"
	// Migrating means a declared migration hook is running.
	Migrating State = "migrating"
	// Deploying means the workload is being applied.
	Deploying State = "deploying"
	// Verifying means the workload is rolled out and declarative checks run.
	Verifying State = "verifying"
	// Active means the release is serving traffic and verified.
	Active State = "active"
	// Failed means the release did not complete. The failed state is kept for
	// inspection; it is not a claim that the previous release was restored.
	Failed State = "failed"
	// Recovering means recovery to a previous release is in progress.
	Recovering State = "recovering"
	// RolledBack means the previous known-good release was restored.
	RolledBack State = "rolled_back"
)

var transitions = map[State][]State{
	Prepared:   {Migrating, Deploying, Active, Failed, RolledBack},
	Migrating:  {Deploying, Failed, Recovering, RolledBack},
	Deploying:  {Verifying, Active, Failed, Recovering, RolledBack},
	Verifying:  {Active, Failed, Recovering, RolledBack},
	Active:     {Failed},
	Failed:     {Recovering, RolledBack},
	Recovering: {RolledBack, Failed},
	RolledBack: {},
}

// Valid reports whether s is a known release state.
func (s State) Valid() bool {
	_, ok := transitions[s]
	return ok
}

// Terminal reports whether a release in this state is finished.
func (s State) Terminal() bool {
	switch s {
	case Active, RolledBack:
		return true
	}
	return false
}

// CanTransition reports whether a release may move from one state to another.
// Moving to the same state is allowed for idempotent updates.
func CanTransition(from, to State) bool {
	if from == to {
		return from.Valid()
	}
	for _, next := range transitions[from] {
		if next == to {
			return true
		}
	}
	return false
}

// States returns the known states in lifecycle order.
func States() []State {
	return []State{Prepared, Migrating, Deploying, Verifying, Active, Failed, Recovering, RolledBack}
}

// RecoveryPolicy controls what happens after a qualifying failure.
type RecoveryPolicy string

const (
	// RecoveryPrevious attempts to restore the previous known-good release.
	RecoveryPrevious RecoveryPolicy = "previous"
	// RecoveryNone keeps the failed release for inspection.
	RecoveryNone RecoveryPolicy = "none"
	// RecoveryRequireApproval prepares recovery and waits for an operator.
	RecoveryRequireApproval RecoveryPolicy = "require-approval"
)

// ValidRecoveryPolicy reports whether p is a supported policy.
func ValidRecoveryPolicy(p RecoveryPolicy) bool {
	switch p {
	case RecoveryPrevious, RecoveryNone, RecoveryRequireApproval:
		return true
	}
	return false
}

// RecoveryTrigger names a qualifying failure for recovery.
type RecoveryTrigger string

const (
	TriggerDeployError        RecoveryTrigger = "deploy-error"
	TriggerVerificationFailed RecoveryTrigger = "verification-failure"
	TriggerTimeout            RecoveryTrigger = "timeout"
	TriggerInterrupted        RecoveryTrigger = "interrupted"
)

// ValidTrigger reports whether t is a supported trigger.
func ValidTrigger(t RecoveryTrigger) bool {
	switch t {
	case TriggerDeployError, TriggerVerificationFailed, TriggerTimeout, TriggerInterrupted:
		return true
	}
	return false
}

// ShouldRecover reports whether a failure with the given trigger qualifies for
// recovery under a policy. require-approval never recovers automatically.
func ShouldRecover(policy RecoveryPolicy, triggers []RecoveryTrigger, trigger RecoveryTrigger) bool {
	if policy != RecoveryPrevious {
		return false
	}
	for _, t := range triggers {
		if t == trigger {
			return true
		}
	}
	return false
}
