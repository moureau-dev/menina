package infrastructure

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
)

const K3sInstallScript = "https://get.k3s.io"

type K3s struct {
	Runner CommandRunner
	Log    *slog.Logger
}

func NewK3s(runner CommandRunner, log *slog.Logger) *K3s {
	if runner == nil {
		runner = ExecRunner{}
	}
	return &K3s{Runner: runner, Log: log}
}

func (k *K3s) IsInstalled(ctx context.Context) bool {
	if _, err := k.Runner.Run(ctx, "k3s", "--version"); err == nil {
		return true
	}
	out, err := k.Runner.Run(ctx, "systemctl", "is-active", "k3s")
	return err == nil && strings.TrimSpace(out) == "active"
}

func (k *K3s) Install(ctx context.Context, disableTraefik bool) error {
	exec := "server"
	if disableTraefik {
		exec = "server --disable traefik --disable servicelb"
	}
	script := fmt.Sprintf(`curl -sfL %s | INSTALL_K3S_EXEC=%q sh -`, K3sInstallScript, exec)
	k.Log.Info("installing k3s", "disableTraefik", disableTraefik)
	out, err := k.Runner.Run(ctx, "sh", "-c", script)
	if err != nil {
		return fmt.Errorf("k3s install failed: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}

func (k *K3s) EnsureInstalled(ctx context.Context, disableTraefik bool) error {
	if k.IsInstalled(ctx) {
		k.Log.Info("k3s already installed; leaving it untouched")
		return nil
	}
	return k.Install(ctx, disableTraefik)
}

func (k *K3s) WriteRegistriesConfig(ctx context.Context, path, content string) error {
	script := fmt.Sprintf("mkdir -p $(dirname %q) && cat > %q <<'EOF'\n%s\nEOF", path, path, content)
	out, err := k.Runner.Run(ctx, "sh", "-c", script)
	if err != nil {
		return fmt.Errorf("write registries.yaml: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}

func PortFree(ctx context.Context, port int) error {
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		return fmt.Errorf("host port %d is not free: %w", port, err)
	}
	_ = ln.Close()
	return nil
}

func ParseListenPort(listen string) (int, error) {
	_, portStr, err := net.SplitHostPort(listen)
	if err != nil {
		return 0, fmt.Errorf("invalid listen address %q: %w", listen, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return 0, fmt.Errorf("invalid port in %q: %w", listen, err)
	}
	return port, nil
}
