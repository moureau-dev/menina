package host

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/core/identity"
)

func newWhoamiCommand(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the server-derived actor for this session",
		RunE: func(cmd *cobra.Command, args []string) error {
			actor := struct {
				ID     string   `json:"id"`
				Source string   `json:"source"`
				Name   string   `json:"name,omitempty"`
				Scopes []string `json:"scopes,omitempty"`
				Admin  bool     `json:"admin,omitempty"`
			}{ID: "local", Source: "local", Admin: true}
			if id := os.Getenv("MENINA_PROFILE_ID"); id != "" {
				cfg, err := app.LoadConfig()
				if err != nil {
					return err
				}
				db, err := app.OpenDB(cfg)
				if err != nil {
					return err
				}
				profile, ok, err := db.ProfileByID(cmd.Context(), id)
				if err != nil {
					return err
				}
				if !ok {
					return identity.ErrEnrollmentInvalid
				}
				actor = struct {
					ID     string   `json:"id"`
					Source string   `json:"source"`
					Name   string   `json:"name,omitempty"`
					Scopes []string `json:"scopes,omitempty"`
					Admin  bool     `json:"admin,omitempty"`
				}{ID: profile.ID, Source: os.Getenv("MENINA_ACTOR_SOURCE"), Name: profile.Name, Scopes: profile.Scopes}
			}
			if app.Opts.JSON {
				return app.PrintJSON(actor)
			}
			if actor.Admin {
				app.Infof("local (administrative)")
				return nil
			}
			app.Infof("%s (%s) scopes=%v", actor.Name, actor.ID, actor.Scopes)
			return nil
		},
	}
}
