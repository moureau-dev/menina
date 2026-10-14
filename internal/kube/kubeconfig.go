package kube

import (
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type rawKubeconfig struct {
	CurrentContext string `yaml:"current-context"`
	Clusters       []struct {
		Name    string `yaml:"name"`
		Cluster struct {
			Server   string `yaml:"server"`
			CAFile   string `yaml:"certificate-authority"`
			CAData   string `yaml:"certificate-authority-data"`
			Insecure bool   `yaml:"insecure-skip-tls-verify"`
		} `yaml:"cluster"`
	} `yaml:"clusters"`
	Users []struct {
		Name string `yaml:"name"`
		User struct {
			Token     string `yaml:"token"`
			TokenFile string `yaml:"tokenFile"`
			CertFile  string `yaml:"client-certificate"`
			CertData  string `yaml:"client-certificate-data"`
			KeyFile   string `yaml:"client-key"`
			KeyData   string `yaml:"client-key-data"`
			Username  string `yaml:"username"`
			Password  string `yaml:"password"`
			Exec      any    `yaml:"exec"`
			AuthProv  any    `yaml:"auth-provider"`
		} `yaml:"user"`
	} `yaml:"users"`
	Contexts []struct {
		Name    string `yaml:"name"`
		Context struct {
			Cluster   string `yaml:"cluster"`
			User      string `yaml:"user"`
			Namespace string `yaml:"namespace"`
		} `yaml:"context"`
	} `yaml:"contexts"`
}

// Load resolves a kubeconfig the same way the previous client-go wiring did:
// explicit path, $KUBECONFIG, ~/.kube/config, the k3s/rke2 defaults, then
// in-cluster.
func Load(path string) (*Config, error) {
	var candidates []string
	if path != "" {
		candidates = append(candidates, path)
	}
	for _, p := range strings.Split(os.Getenv("KUBECONFIG"), string(os.PathListSeparator)) {
		if p != "" {
			candidates = append(candidates, p)
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".kube", "config"))
	}
	candidates = append(candidates, "/etc/rancher/k3s/k3s.yaml", "/etc/rancher/rke2/rke2.yaml")

	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return loadFile(candidate)
		}
	}
	if cfg, err := loadInCluster(); err == nil {
		return cfg, nil
	}
	return nil, fmt.Errorf("no kubeconfig found (checked explicit path, KUBECONFIG, ~/.kube/config, k3s/rke2 defaults, and in-cluster)")
}

func loadFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read kubeconfig %s: %w", path, err)
	}
	cfg, err := parseKubeconfig(data, filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("parse kubeconfig %s: %w", path, err)
	}
	return cfg, nil
}

// Parse builds a Config from kubeconfig YAML bytes (embedded data only; no file
// resolution). Used for the scoped kubeconfig Menina generates in memory.
func Parse(data []byte) (*Config, error) {
	return parseKubeconfig(data, "")
}

func parseKubeconfig(data []byte, dir string) (*Config, error) {
	var raw rawKubeconfig
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	clusterName, userName, namespace := "", "", ""
	for _, c := range raw.Contexts {
		if c.Name == raw.CurrentContext {
			clusterName = c.Context.Cluster
			userName = c.Context.User
			namespace = c.Context.Namespace
			break
		}
	}
	cfg := &Config{Namespace: namespace}
	for _, c := range raw.Clusters {
		if c.Name != clusterName {
			continue
		}
		cfg.Server = c.Cluster.Server
		cfg.Insecure = c.Cluster.Insecure
		if c.Cluster.CAData != "" {
			cfg.CAData, _ = base64.StdEncoding.DecodeString(c.Cluster.CAData)
		} else if c.Cluster.CAFile != "" {
			cfg.CAData, _ = os.ReadFile(resolveRelative(dir, c.Cluster.CAFile))
		}
	}
	for _, u := range raw.Users {
		if u.Name != userName {
			continue
		}
		if u.User.Exec != nil || u.User.AuthProv != nil {
			return nil, fmt.Errorf("kubeconfig user %q uses an exec/auth-provider credential, which Menina does not support; use a token or client certificate", userName)
		}
		cfg.Username = u.User.Username
		cfg.Password = u.User.Password
		switch {
		case u.User.Token != "":
			cfg.Token = u.User.Token
		case u.User.TokenFile != "":
			if b, err := os.ReadFile(resolveRelative(dir, u.User.TokenFile)); err == nil {
				cfg.Token = strings.TrimSpace(string(b))
			}
		}
		if u.User.CertData != "" {
			cfg.CertData, _ = base64.StdEncoding.DecodeString(u.User.CertData)
		} else if u.User.CertFile != "" {
			cfg.CertData, _ = os.ReadFile(resolveRelative(dir, u.User.CertFile))
		}
		if u.User.KeyData != "" {
			cfg.KeyData, _ = base64.StdEncoding.DecodeString(u.User.KeyData)
		} else if u.User.KeyFile != "" {
			cfg.KeyData, _ = os.ReadFile(resolveRelative(dir, u.User.KeyFile))
		}
	}
	if cfg.Server == "" {
		return nil, fmt.Errorf("could not resolve server for context %q", raw.CurrentContext)
	}
	return cfg, nil
}

func resolveRelative(dir, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(dir, p)
}

func loadInCluster() (*Config, error) {
	host := os.Getenv("KUBERNETES_SERVICE_HOST")
	port := os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, fmt.Errorf("not running in-cluster")
	}
	dir := "/var/run/secrets/kubernetes.io/serviceaccount"
	token, err := os.ReadFile(filepath.Join(dir, "token"))
	if err != nil {
		return nil, fmt.Errorf("read service account token: %w", err)
	}
	ca, _ := os.ReadFile(filepath.Join(dir, "ca.crt"))
	ns, _ := os.ReadFile(filepath.Join(dir, "namespace"))
	return &Config{
		Server:    "https://" + net.JoinHostPort(host, port),
		Token:     strings.TrimSpace(string(token)),
		CAData:    ca,
		Namespace: strings.TrimSpace(string(ns)),
	}, nil
}
