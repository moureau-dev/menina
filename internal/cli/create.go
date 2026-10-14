package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newCreateCommand(app *App) *cobra.Command {
	var namespace, description string
	var yes bool
	cmd := &cobra.Command{
		Use:   "create [project-name]",
		Short: "Create a project (prompts when no name is given)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := newPrompter()
			name := ""
			if len(args) > 0 {
				name = args[0]
			}
			if name == "" {
				if !p.enabled || yes {
					return fmt.Errorf("project name is required (or run on a terminal)")
				}
				name = p.Ask("Project name", "")
			}
			if name == "" {
				return fmt.Errorf("project name is required")
			}
			if namespace == "" && p.enabled && !yes {
				namespace = p.Ask("Namespace (blank = name)", name)
			}
			if description == "" && p.enabled && !yes {
				description = p.Ask("Description (optional)", "")
			}
			if err := createProject(app, name, namespace, description); err != nil {
				return err
			}
			if p.enabled && !yes && p.Confirm(fmt.Sprintf("Create an app in %q now?", name), false) {
				if _, err := createAppInline(app, p, name); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&namespace, "namespace", "", "Kubernetes namespace for workloads")
	cmd.Flags().StringVar(&description, "description", "", "project description")
	cmd.Flags().BoolVar(&yes, "yes", false, "non-interactive; use flags only")
	return cmd
}
