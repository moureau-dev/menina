package infrastructure

import (
	"context"
	"fmt"
	"os/user"
	"strings"
)

type UserManager struct {
	Runner CommandRunner
}

func NewUserManager(runner CommandRunner) *UserManager {
	if runner == nil {
		runner = ExecRunner{}
	}
	return &UserManager{Runner: runner}
}

func (u *UserManager) Exists(name string) bool {
	_, err := user.Lookup(name)
	return err == nil
}

// Ensure creates a system user (and its primary group) if it does not exist.
// It is idempotent and safe to call from `menina deploy`.
func (u *UserManager) Ensure(ctx context.Context, name string) error {
	if name == "" || u.Exists(name) {
		return nil
	}
	if out, err := u.Runner.Run(ctx, "useradd", "--system", "--no-create-home", "--shell", "/usr/sbin/nologin", "--user-group", name); err == nil {
		return nil
	} else {
		out2, err2 := u.Runner.Run(ctx, "adduser", "--system", "--no-create-home", "--group", name)
		if err2 != nil {
			return fmt.Errorf("create system user %q: %w (%s)", name, err, strings.TrimSpace(out+" "+out2))
		}
	}
	return nil
}

// AddToGroup adds an existing user to a supplementary group (idempotent).
func (u *UserManager) AddToGroup(ctx context.Context, userName, group string) error {
	if userName == "" || userName == "root" {
		return nil
	}
	if out, err := u.Runner.Run(ctx, "usermod", "-aG", group, userName); err != nil {
		return fmt.Errorf("add %q to group %q: %w (%s)", userName, group, err, strings.TrimSpace(out))
	}
	return nil
}
