package client

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Context is one stored SSH target.
type Context struct {
	Host    string `yaml:"host"`
	HostKey string `yaml:"hostKey,omitempty"`
}

// ContextFile is the local client context configuration.
type ContextFile struct {
	Current  string             `yaml:"current,omitempty"`
	Contexts map[string]Context `yaml:"contexts"`
}

func contextsPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "menina", "contexts.yml"), nil
}

func loadContexts() (ContextFile, error) {
	path, err := contextsPath()
	if err != nil {
		return ContextFile{}, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ContextFile{Contexts: map[string]Context{}}, nil
	}
	if err != nil {
		return ContextFile{}, err
	}
	var file ContextFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return ContextFile{}, err
	}
	if file.Contexts == nil {
		file.Contexts = map[string]Context{}
	}
	return file, nil
}

func saveContexts(file ContextFile) error {
	path, err := contextsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(file)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func (a *App) contextName(flagProfile string) string {
	if flagProfile != "" {
		return flagProfile
	}
	if a.opts.Profile != nil && *a.opts.Profile != "" {
		return *a.opts.Profile
	}
	if value := os.Getenv("MENINA_PROFILE"); value != "" {
		return value
	}
	file, err := loadContexts()
	if err == nil {
		return file.Current
	}
	return ""
}

func (a *App) resolveHost(flagHost, flagProfile string) (host, contextName, hostKey string) {
	name := a.contextName(flagProfile)
	file, _ := loadContexts()
	ctx := file.Contexts[name]
	if flagHost != "" {
		_ = writeCachedHost(flagHost)
		return flagHost, name, matchingHostKey(file, flagHost)
	}
	if value := os.Getenv("MENINA_HOST"); value != "" {
		return value, name, matchingHostKey(file, value)
	}
	if name != "" && ctx.Host != "" {
		return ctx.Host, name, ctx.HostKey
	}
	cached, _ := readCachedHost()
	if cached != "" {
		return cached, name, matchingHostKey(file, cached)
	}
	return "", name, ""
}

func matchingHostKey(file ContextFile, host string) string {
	for _, ctx := range file.Contexts {
		if ctx.Host == host {
			return ctx.HostKey
		}
	}
	return ""
}

func (a *App) chooseClientHost(flagHost, flagProfile string) string {
	host, _, _ := a.resolveHost(flagHost, flagProfile)
	return host
}

func sshKnownHost(host string) string {
	h := host
	if idx := strings.LastIndex(h, "@"); idx >= 0 {
		h = h[idx+1:]
	}
	if strings.HasPrefix(h, "[") {
		return h
	}
	if strings.Count(h, ":") == 1 {
		parts := strings.SplitN(h, ":", 2)
		return "[" + parts[0] + "]:" + parts[1]
	}
	return h
}

func sshPinArgs(host, hostKey string) ([]string, func()) {
	if strings.TrimSpace(hostKey) == "" {
		return nil, func() {}
	}
	dir, err := os.MkdirTemp("", "menina-known-hosts")
	if err != nil {
		return nil, func() {}
	}
	path := filepath.Join(dir, "known_hosts")
	line := sshKnownHost(host) + " " + strings.TrimSpace(hostKey) + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return nil, func() {}
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	return []string{"-o", "UserKnownHostsFile=" + path, "-o", "StrictHostKeyChecking=yes"}, cleanup
}

func (a *App) sshArgs(host, flagProfile string, extra ...string) ([]string, func()) {
	file, _ := loadContexts()
	pin, cleanup := sshPinArgs(host, matchingHostKey(file, host))
	args := append([]string{}, pin...)
	args = append(args, extra...)
	return args, cleanup
}

func (a *App) addContext(name string, ctx Context) error {
	if name == "" {
		return fmt.Errorf("context name is required")
	}
	file, err := loadContexts()
	if err != nil {
		return err
	}
	file.Contexts[name] = ctx
	if file.Current == "" {
		file.Current = name
	}
	return saveContexts(file)
}
