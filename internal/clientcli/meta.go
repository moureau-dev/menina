package clientcli

import (
	"encoding/json"
	"os"
	"runtime"

	"github.com/spf13/cobra"

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

func newCapabilitiesCommand(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "capabilities",
		Short: "Print the client's capabilities for automation",
		Long: "Report local CLI, platform, manifest schema and feature capabilities.\n" +
			"This is intentionally local-only; client/host compatibility checks are a\n" +
			"separate feature.",
		Example: "  menina capabilities --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			info := version.Get()
			caps := map[string]any{
				"version": info,
				"os":      runtime.GOOS,
				"arch":    runtime.GOARCH,
				"manifestSchema": map[string]any{
					"version": manifest.CurrentVersion,
					"url":     schema.ManifestURL,
				},
				"features": map[string]bool{
					"manifest":       true,
					"vars":           true,
					"from":           true,
					"buildArgs":      true,
					"service":        true,
					"exposureHttp":   true,
					"exposurePorts":  true,
					"secrets":        true,
					"install":        true,
					"upgrade":        true,
					"tui":            true,
					"applyCheck":     true,
					"applyDiff":      false,
					"applyVerify":    false,
					"nonInteractive": true,
				},
				"commands": []string{
					"init", "install", "validate", "apply", "build", "push",
					"secret", "schema", "capabilities", "version", "upgrade",
				},
			}
			if app.jsonOutput() {
				return app.printJSON(caps)
			}
			app.infof("menina %s (%s/%s)", info.Version, runtime.GOOS, runtime.GOARCH)
			app.infof("manifest schema: v%d %s", manifest.CurrentVersion, schema.ManifestURL)
			app.infof("features: buildArgs, exposure.http, exposure.ports, secrets, install, upgrade, non-interactive")
			return nil
		},
	}
}
