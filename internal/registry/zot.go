package registry

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/moureau-dev/menina/internal/config"
)

type ZotStorage struct {
	RootDirectory string        `json:"rootDirectory"`
	Dedupe        bool          `json:"dedupe,omitempty"`
	GC            bool          `json:"gc,omitempty"`
	Retention     *ZotRetention `json:"retention,omitempty"`
}

type ZotRetention struct {
	Policies []ZotRetentionPolicy `json:"policies"`
}

type ZotRetentionPolicy struct {
	Repositories   []string            `json:"repositories,omitempty"`
	KeepTags       []ZotKeepTagsPolicy `json:"keepTags,omitempty"`
	DeleteUntagged bool                `json:"deleteUntagged"`
}

type ZotKeepTagsPolicy struct {
	Patterns                []string `json:"patterns,omitempty"`
	MostRecentlyPushedCount int      `json:"mostRecentlyPushedCount,omitempty"`
}

type ZotAuthHtpasswd struct {
	Path string `json:"path"`
}

type ZotAuth struct {
	Htpasswd ZotAuthHtpasswd `json:"htpasswd"`
}

type ZotHTTP struct {
	Address string   `json:"address"`
	Port    string   `json:"port"`
	Auth    ZotAuth  `json:"auth"`
	Compat  []string `json:"compat,omitempty"`
}

type ZotLog struct {
	Level string `json:"level"`
}

type ZotEventSink struct {
	Type    string            `json:"type"`
	Address string            `json:"address"`
	Headers map[string]string `json:"headers,omitempty"`
	Timeout string            `json:"timeout,omitempty"`
}

type ZotEvents struct {
	Enable bool           `json:"enable"`
	Sinks  []ZotEventSink `json:"sinks,omitempty"`
}

type ZotExtensions struct {
	Events *ZotEvents `json:"events,omitempty"`
}

type ZotConfig struct {
	DistSpecVersion string        `json:"distSpecVersion"`
	Storage         ZotStorage    `json:"storage"`
	HTTP            ZotHTTP       `json:"http"`
	Log             ZotLog        `json:"log"`
	Extensions      ZotExtensions `json:"extensions"`
}

type ZotParams struct {
	RootDirectory    string
	ContainerPort    string
	HtpasswdPath     string
	EventAddress     string
	EventToken       string
	RetentionEnabled bool
	KeepRecentTags   int
	ProtectedTags    []string
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
			Auth:    ZotAuth{Htpasswd: ZotAuthHtpasswd{Path: p.HtpasswdPath}},
			Compat:  []string{"docker2s2"},
		},
		Log: ZotLog{Level: "info"},
	}
	if p.RetentionEnabled && p.KeepRecentTags > 0 {
		cfg.Storage.Retention = &ZotRetention{
			Policies: []ZotRetentionPolicy{{
				Repositories: []string{"**"},
				KeepTags: []ZotKeepTagsPolicy{{
					Patterns:                []string{".*"},
					MostRecentlyPushedCount: p.KeepRecentTags,
				}},
				// Never delete untagged manifests: a retained release may be
				// pinned only by digest (its tag moved or was overwritten), and
				// garbage collection must keep the image a rollback needs.
				DeleteUntagged: false,
			}},
		}
	}
	if p.EventAddress != "" {
		headers := map[string]string{"Content-Type": "application/json"}
		if p.EventToken != "" {
			headers["Authorization"] = "Bearer " + p.EventToken
		}
		cfg.Extensions.Events = &ZotEvents{
			Enable: true,
			Sinks: []ZotEventSink{{
				Type:    "http",
				Address: p.EventAddress,
				Headers: headers,
				Timeout: "10s",
			}},
		}
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// ValidateAgainstZotVersion ensures the pinned Zot version supports the
// configuration Menina emits. The CloudEvents-based events extension was added
// in v2.1.3; earlier versions reject `extensions.events`.
func ValidateAgainstZotVersion(version string) error {
	major, minor, patch, err := parseZotVersion(version)
	if err != nil {
		return err
	}
	if major != 2 {
		return fmt.Errorf("unsupported Zot version %q: Menina targets the Zot v2 configuration schema", version)
	}
	if minor < 1 || (minor == 1 && patch < 3) {
		return fmt.Errorf("unsupported Zot version %q: image-push events require Zot >= v2.1.3", version)
	}
	return nil
}

func parseZotVersion(version string) (major, minor, patch int, err error) {
	v := strings.TrimPrefix(strings.TrimSpace(version), "v")
	parts := strings.Split(v, ".")
	if len(parts) < 2 {
		return 0, 0, 0, fmt.Errorf("invalid Zot version %q", version)
	}
	if major, err = strconv.Atoi(parts[0]); err != nil {
		return 0, 0, 0, fmt.Errorf("invalid Zot version %q", version)
	}
	if minor, err = strconv.Atoi(parts[1]); err != nil {
		return 0, 0, 0, fmt.Errorf("invalid Zot version %q", version)
	}
	if len(parts) >= 3 {
		patchPart := parts[2]
		if i := strings.IndexAny(patchPart, "-+"); i >= 0 {
			patchPart = patchPart[:i]
		}
		patch, _ = strconv.Atoi(patchPart)
	}
	return major, minor, patch, nil
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
