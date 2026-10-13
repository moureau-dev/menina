package infrastructure

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Systemd struct {
	Runner  CommandRunner
	UnitDir string
}

func NewSystemd(runner CommandRunner) *Systemd {
	if runner == nil {
		runner = ExecRunner{}
	}
	return &Systemd{Runner: runner, UnitDir: "/etc/systemd/system"}
}

func (s *Systemd) WriteUnit(name, content string) error {
	if err := os.MkdirAll(s.UnitDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(s.UnitDir, name)
	return os.WriteFile(path, []byte(content), 0o644)
}

func (s *Systemd) RemoveUnit(name string) error {
	path := filepath.Join(s.UnitDir, name)
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (s *Systemd) DaemonReload(ctx context.Context) error {
	_, err := s.Runner.Run(ctx, "systemctl", "daemon-reload")
	return err
}

func (s *Systemd) Enable(ctx context.Context, name string) error {
	_, err := s.Runner.Run(ctx, "systemctl", "enable", name)
	return err
}

func (s *Systemd) Restart(ctx context.Context, name string) error {
	if _, err := s.Runner.Run(ctx, "systemctl", "restart", name); err != nil {
		return err
	}
	return nil
}

func (s *Systemd) Start(ctx context.Context, name string) error {
	_, err := s.Runner.Run(ctx, "systemctl", "start", name)
	return err
}

func (s *Systemd) Stop(ctx context.Context, name string) error {
	_, err := s.Runner.Run(ctx, "systemctl", "stop", name)
	return err
}

func (s *Systemd) IsActive(ctx context.Context, name string) bool {
	out, err := s.Runner.Run(ctx, "systemctl", "is-active", name)
	return err == nil && strings.TrimSpace(out) == "active"
}

func (s *Systemd) Status(ctx context.Context, name string) string {
	out, _ := s.Runner.Run(ctx, "systemctl", "status", "--no-pager", name)
	return out
}

func (s *Systemd) Apply(ctx context.Context, name, content string) error {
	if err := s.WriteUnit(name, content); err != nil {
		return fmt.Errorf("write unit %s: %w", name, err)
	}
	if err := s.DaemonReload(ctx); err != nil {
		return fmt.Errorf("daemon-reload: %w", err)
	}
	if err := s.Enable(ctx, name); err != nil {
		return fmt.Errorf("enable %s: %w", name, err)
	}
	if err := s.Restart(ctx, name); err != nil {
		return fmt.Errorf("restart %s: %w", name, err)
	}
	return nil
}
