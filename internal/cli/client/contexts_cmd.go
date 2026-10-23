package client

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"
)

func newContextCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{Use: "context", Short: "Manage stored client contexts"}
	cmd.AddCommand(contextSet(app), contextList(app), contextUse(app), contextDelete(app))
	return cmd
}

func contextSet(app *App) *cobra.Command {
	var host, hostKey string
	cmd := &cobra.Command{
		Use:   "set <name>",
		Args:  cobra.ExactArgs(1),
		Short: "Create or update a stored client context",
		RunE: func(cmd *cobra.Command, args []string) error {
			if host == "" {
				return fmt.Errorf("--host is required")
			}
			if err := app.addContext(args[0], Context{Host: host, HostKey: hostKey}); err != nil {
				return err
			}
			app.infof("Saved context %s", args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "SSH host (user@host)")
	cmd.Flags().StringVar(&hostKey, "host-key", "", "pinned host public key")
	return cmd
}

func contextList(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List stored client contexts",
		RunE: func(cmd *cobra.Command, args []string) error {
			file, err := loadContexts()
			if err != nil {
				return err
			}
			if app.jsonOutput() {
				return app.printJSON(file)
			}
			names := make([]string, 0, len(file.Contexts))
			for name := range file.Contexts {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				ctx := file.Contexts[name]
				marker := " "
				if name == file.Current {
					marker = "*"
				}
				app.infof("%s %s\t%s", marker, name, ctx.Host)
			}
			return nil
		},
	}
}

func contextUse(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "use <name>",
		Args:  cobra.ExactArgs(1),
		Short: "Select the default client context",
		RunE: func(cmd *cobra.Command, args []string) error {
			file, err := loadContexts()
			if err != nil {
				return err
			}
			if _, ok := file.Contexts[args[0]]; !ok {
				return fmt.Errorf("context %q not found", args[0])
			}
			file.Current = args[0]
			if err := saveContexts(file); err != nil {
				return err
			}
			app.infof("Using context %s", args[0])
			return nil
		},
	}
}

func contextDelete(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <name>",
		Args:  cobra.ExactArgs(1),
		Short: "Delete a stored client context",
		RunE: func(cmd *cobra.Command, args []string) error {
			file, err := loadContexts()
			if err != nil {
				return err
			}
			delete(file.Contexts, args[0])
			if file.Current == args[0] {
				file.Current = ""
			}
			if err := saveContexts(file); err != nil {
				return err
			}
			app.infof("Deleted context %s", args[0])
			return nil
		},
	}
}

func newWhoamiCommand(app *App) *cobra.Command {
	var host string
	cmd := &cobra.Command{
		Use:   "whoami",
		Short: "Show the server-derived identity for the selected context",
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := app.requireClientHost(host)
			if err != nil {
				return err
			}
			return app.runSSHInherit(cmd, target, "whoami")
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "SSH host (user@host)")
	return cmd
}
