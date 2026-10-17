package clientcli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/clierr"
	"github.com/moureau-dev/menina/internal/manifest"
	"github.com/moureau-dev/menina/internal/schema"
	"github.com/moureau-dev/menina/internal/version"
)

func newSchemaCommand(app *App) *cobra.Command {
	var urlOnly bool
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "Print the .menina.yml JSON Schema",
		Long: "Print the manifest JSON Schema embedded in this binary. With --json it\n" +
			"wraps the schema in metadata for agents; --url prints only the public URL.",
		Example: "  menina schema\n  menina schema --json\n  menina schema --url",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if urlOnly {
				if app.jsonOutput() {
					return app.printJSON(map[string]any{"schemaUrl": schema.ManifestURL})
				}
				app.infof("%s", schema.ManifestURL)
				return nil
			}
			if !app.jsonOutput() {
				_, err := os.Stdout.Write(schema.Manifest)
				return err
			}
			var doc any
			if err := json.Unmarshal(schema.Manifest, &doc); err != nil {
				return err
			}
			return app.printJSON(map[string]any{
				"schemaVersion": manifest.CurrentVersion,
				"schemaUrl":     schema.ManifestURL,
				"schema":        doc,
			})
		},
	}
	cmd.Flags().BoolVar(&urlOnly, "url", false, "print only the public schema URL")
	return cmd
}

type manifestSchemaCap struct {
	Version int    `json:"version"`
	URL     string `json:"url"`
}

type capabilityDoc struct {
	Version        version.Info      `json:"version"`
	OS             string            `json:"os"`
	Arch           string            `json:"arch"`
	ManifestSchema manifestSchemaCap `json:"manifestSchema"`
	Features       map[string]bool   `json:"features"`
	Commands       []string          `json:"commands"`
}

type compatibilityDoc struct {
	SchemaMatch  bool     `json:"schemaMatch"`
	VersionMatch bool     `json:"versionMatch"`
	Compatible   bool     `json:"compatible"`
	Warnings     []string `json:"warnings"`
}

func localCapabilities() capabilityDoc {
	return capabilityDoc{
		Version: version.Get(),
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
		ManifestSchema: manifestSchemaCap{
			Version: manifest.CurrentVersion,
			URL:     schema.ManifestURL,
		},
		Features: map[string]bool{
			"manifest":        true,
			"vars":            true,
			"from":            true,
			"buildArgs":       true,
			"service":         true,
			"exposureHttp":    true,
			"exposurePorts":   true,
			"secrets":         true,
			"install":         true,
			"upgrade":         true,
			"tui":             true,
			"applyCheck":      true,
			"applyDiff":       true,
			"applyVerify":     true,
			"applyPrune":      true,
			"sourceOwnership": true,
			"configRevisions": true,
			"jobWatch":        true,
			"nonInteractive":  true,
		},
		Commands: []string{
			"init", "install", "validate", "apply", "build", "push",
			"secret", "schema", "capabilities", "version", "upgrade",
		},
	}
}

func newCapabilitiesCommand(app *App) *cobra.Command {
	var host string
	cmd := &cobra.Command{
		Use:   "capabilities",
		Short: "Print the client's capabilities for automation",
		Long: "Report local CLI, platform, manifest schema and feature capabilities.\n" +
			"With --host, also query the host binary over SSH and report compatibility.\n" +
			"Use --json for a stable machine-readable document.",
		Example: "  menina capabilities --json\n  menina capabilities --host root@vps --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			local := localCapabilities()
			out := map[string]any{}
			encoded, _ := json.Marshal(local)
			_ = json.Unmarshal(encoded, &out)
			var warnings []string

			if host != "" {
				if strings.HasPrefix(host, "-") {
					return fmt.Errorf("invalid SSH host %q", host)
				}
				hostDoc, err := remoteCapabilities(cmd, host)
				if err != nil {
					return clierr.WithHint(clierr.Wrap(clierr.CodeUnsupported, err), "the host may be older or missing `menina capabilities`; upgrade the host binary")
				}
				compat := compatibilityDoc{
					SchemaMatch:  local.ManifestSchema.Version == hostDoc.ManifestSchema.Version,
					VersionMatch: local.Version.Version == hostDoc.Version.Version,
					Compatible:   local.ManifestSchema.Version == hostDoc.ManifestSchema.Version,
				}
				if !compat.SchemaMatch {
					compat.Warnings = append(compat.Warnings, fmt.Sprintf("manifest schema mismatch: client v%d, host v%d", local.ManifestSchema.Version, hostDoc.ManifestSchema.Version))
				}
				if !compat.VersionMatch {
					compat.Warnings = append(compat.Warnings, fmt.Sprintf("client/host version differs: client %s, host %s", local.Version.Version, hostDoc.Version.Version))
				}
				out["host"] = hostDoc
				out["compatibility"] = compat
				warnings = compat.Warnings
			}

			if app.jsonOutput() {
				return app.printJSON(out)
			}
			app.infof("menina %s (%s/%s)", local.Version.Version, local.OS, local.Arch)
			app.infof("manifest schema: v%d %s", local.ManifestSchema.Version, local.ManifestSchema.URL)
			if host != "" {
				app.infof("host %s: menina %s (%s/%s), schema v%d", host, out["host"].(capabilityDoc).Version.Version, out["host"].(capabilityDoc).OS, out["host"].(capabilityDoc).Arch, out["host"].(capabilityDoc).ManifestSchema.Version)
				for _, w := range warnings {
					app.infof("warning: %s", w)
				}
				if len(warnings) == 0 {
					app.infof("compatibility: ok")
				}
			}
			app.infof("features: buildArgs, exposure.http, exposure.ports, secrets, install, upgrade, non-interactive")
			return nil
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "SSH host to query for host capabilities")
	return cmd
}

func remoteCapabilities(cmd *cobra.Command, host string) (capabilityDoc, error) {
	var doc capabilityDoc
	c := exec.CommandContext(cmd.Context(), "ssh", "-T", host, "menina", "capabilities", "--json")
	out, err := c.CombinedOutput()
	if err != nil {
		return doc, fmt.Errorf("ssh %s: %v: %s", host, err, strings.TrimSpace(string(out)))
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return doc, fmt.Errorf("parse host capabilities: %w", err)
	}
	return doc, nil
}
