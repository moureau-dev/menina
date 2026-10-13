package registry

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/moureau-dev/menina/internal/config"
)

type ZotStorage struct {
	RootDirectory string `json:"rootDirectory"`
	Dedupe        bool   `json:"dedupe"`
	GC            bool   `json:"gc"`
}

type ZotAuthHtpasswd struct {
	Path string `json:"path"`
}

type ZotAuth struct {
	Htpasswd ZotAuthHtpasswd `json:"htpasswd"`
}

type ZotHTTP struct {
	Address string  `json:"address"`
	Port    string  `json:"port"`
	Auth    ZotAuth `json:"auth"`
}

type ZotLog struct {
	Level string `json:"level"`
}

type ZotEventSink struct {
	Type    string            `json:"type"`
	Address string            `json:"address"`
	Headers map[string]string `json:"headers,omitempty"`
}

type ZotEvents struct {
	Sinks []ZotEventSink `json:"sinks"`
}

type ZotRetainPolicy struct {
	Match  []map[string]string `json:"match"`
	Retain int                 `json:"retain"`
}

type ZotRepositoryPolicy struct {
	Patterns []string          `json:"patterns"`
	Policies []ZotRetainPolicy `json:"policies"`
}

type ZotRetentionPolicy struct {
	Repositories   []ZotRepositoryPolicy `json:"repositories"`
	DeleteUntagged bool                  `json:"deleteUntagged"`
}

type ZotRetention struct {
	Policy ZotRetentionPolicy `json:"policy"`
}

type ZotExtensions struct {
	Retention ZotRetention `json:"retention"`
	Events    ZotEvents    `json:"events"`
}

type ZotConfig struct {
	DistSpecVersion string        `json:"distSpecVersion"`
	Storage         ZotStorage    `json:"storage"`
	HTTP            ZotHTTP       `json:"http"`
	Log             ZotLog        `json:"log"`
	Extensions      ZotExtensions `json:"extensions"`
}

type ZotParams struct {
	RootDirectory  string
	ContainerPort  string
	HtpasswdPath   string
	EventAddress   string
	EventToken     string
	KeepRecentTags int
	ProtectedTags  []string
}

func BuildZotConfig(p ZotParams) ([]byte, error) {
	if p.ContainerPort == "" {
		p.ContainerPort = "5000"
	}
	if p.HtpasswdPath == "" {
		p.HtpasswdPath = "/etc/zot/htpasswd"
	}
	cfg := ZotConfig{
		DistSpecVersion: "1.1.0",
		Storage: ZotStorage{
			RootDirectory: p.RootDirectory,
			Dedupe:        true,
			GC:            true,
		},
		HTTP: ZotHTTP{
			Address: "0.0.0.0",
			Port:    p.ContainerPort,
		},
		Log: ZotLog{Level: "info"},
	}
	if p.HtpasswdPath != "" {
		cfg.HTTP.Auth = ZotAuth{Htpasswd: ZotAuthHtpasswd{Path: p.HtpasswdPath}}
	}
	if p.EventAddress != "" {
		headers := map[string]string{"Content-Type": "application/json"}
		if p.EventToken != "" {
			headers["Authorization"] = "Bearer " + p.EventToken
		}
		cfg.Extensions.Events.Sinks = []ZotEventSink{{
			Type:    "http",
			Address: p.EventAddress,
			Headers: headers,
		}}
	}
	if p.KeepRecentTags > 0 {
		policies := []ZotRetainPolicy{{Match: []map[string]string{{"tag": ".*"}}, Retain: p.KeepRecentTags}}
		cfg.Extensions.Retention = ZotRetention{
			Policy: ZotRetentionPolicy{
				Repositories: []ZotRepositoryPolicy{{
					Patterns: []string{"**"},
					Policies: policies,
				}},
				DeleteUntagged: false,
			},
		}
	}
	return json.MarshalIndent(cfg, "", "  ")
}

func ValidateAgainstZotVersion(version string) error {
	if version == "" {
		return nil
	}
	if !strings.HasPrefix(version, "v2.") {
		return fmt.Errorf("unsupported Zot version %q: Menina emits v2 retention/events configuration", version)
	}
	return nil
}

func TranslateRetention(cfg config.Registry, protectedDigests []string) (int, []string, error) {
	keep := cfg.Retention.KeepRecentTags
	var protected []string
	for _, d := range protectedDigests {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		if !strings.Contains(d, ":") {
			return 0, nil, fmt.Errorf("protected digest %q is not a valid digest (expected algorithm:hex)", d)
		}
		protected = append(protected, d)
	}
	if keep < 0 {
		return 0, nil, fmt.Errorf("registry.retention.keepRecentTags must be >= 0")
	}
	return keep, protected, nil
}
