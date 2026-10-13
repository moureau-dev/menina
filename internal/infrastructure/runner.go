package infrastructure

import (
	"context"
	"os/exec"
	"strings"
)

type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

type FakeRunner struct {
	Commands [][]string
	Outputs  map[string]string
	Errors   map[string]error
}

func NewFakeRunner() *FakeRunner {
	return &FakeRunner{Outputs: map[string]string{}, Errors: map[string]error{}}
}

func (f *FakeRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	f.Commands = append(f.Commands, append([]string{name}, args...))
	key := strings.Join(append([]string{name}, args...), " ")
	return f.Outputs[key], f.Errors[key]
}
