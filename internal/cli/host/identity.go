package host

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/moureau-dev/menina/internal/core/identity"
	"github.com/moureau-dev/menina/internal/core/state/contract"
)

func newIdentityCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "identity",
		Short: "Manage profiles, keys, and enrollment tokens",
		Long:  "Local administrative commands for Menina identities. Remote clients never use these commands.",
	}
	cmd.AddCommand(
		identityProfileCommand(app),
		identityKeyCommand(app),
		identityEnrollCommand(app),
		identityBootstrapCommand(app),
	)
	return cmd
}

func identityProfileCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{Use: "profile", Short: "Manage identity profiles"}
	cmd.AddCommand(identityProfileCreate(app), identityProfileList(app))
	return cmd
}

func identityKeyCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{Use: "key", Short: "Manage profile keys"}
	cmd.AddCommand(identityKeyAdd(app), identityKeyList(app), identityKeyAuthorized(app), identityKeyRevoke(app), identityKeyRotate(app))
	return cmd
}

func identityEnrollCommand(app *App) *cobra.Command {
	cmd := &cobra.Command{Use: "enroll", Short: "Create or consume enrollment tokens"}
	cmd.AddCommand(identityEnrollCreate(app), identityEnrollConsume(app))
	return cmd
}

func identityProfileCreate(app *App) *cobra.Command {
	var scopes []string
	cmd := &cobra.Command{
		Use:   "create <name>",
		Args:  cobra.ExactArgs(1),
		Short: "Create an identity profile",
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openIdentityStore(app)
			if err != nil {
				return err
			}
			id, err := db.CreateProfile(cmd.Context(), identity.Profile{Name: args[0], Scopes: scopes, Status: identity.StatusActive})
			if err != nil {
				return err
			}
			if app.Opts.JSON {
				return app.PrintJSON(map[string]any{"id": id, "name": args[0], "scopes": scopes})
			}
			app.Infof("Created profile %s (%s)", args[0], id)
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&scopes, "scope", nil, "scope to grant (repeatable)")
	return cmd
}

func identityProfileList(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List identity profiles",
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openIdentityStore(app)
			if err != nil {
				return err
			}
			profiles, err := db.Profiles(cmd.Context())
			if err != nil {
				return err
			}
			if app.Opts.JSON {
				return app.PrintJSON(profiles)
			}
			for _, p := range profiles {
				app.Infof("%s\t%s\t%s\t%s", p.ID, p.Name, p.Status, strings.Join(p.Scopes, ","))
			}
			return nil
		},
	}
}

func identityKeyAdd(app *App) *cobra.Command {
	var fingerprint, publicKey, comment, ttl string
	cmd := &cobra.Command{
		Use:   "add <profile-id>",
		Args:  cobra.ExactArgs(1),
		Short: "Attach a key to a profile",
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openIdentityStore(app)
			if err != nil {
				return err
			}
			spec, err := keySpec(fingerprint, publicKey, comment, ttl)
			if err != nil {
				return err
			}
			spec.ProfileID = args[0]
			id, err := db.AddProfileKey(cmd.Context(), spec)
			if err != nil {
				return err
			}
			if app.Opts.JSON {
				return app.PrintJSON(map[string]any{"id": id, "profileId": spec.ProfileID, "fingerprint": spec.Fingerprint, "expiresAt": spec.ExpiresAt})
			}
			app.Infof("Added key %s (%s)", spec.Fingerprint, id)
			return nil
		},
	}
	cmd.Flags().StringVar(&fingerprint, "fingerprint", "", "key fingerprint")
	cmd.Flags().StringVar(&publicKey, "public-key", "", "authorized_keys line or file path")
	cmd.Flags().StringVar(&comment, "comment", "", "key comment")
	cmd.Flags().StringVar(&ttl, "ttl", "", "optional key lifetime, for example 1h")
	return cmd
}

func identityKeyList(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "list <profile-id>",
		Args:  cobra.ExactArgs(1),
		Short: "List keys attached to a profile",
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openIdentityStore(app)
			if err != nil {
				return err
			}
			keys, err := db.ProfileKeys(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if app.Opts.JSON {
				return app.PrintJSON(keys)
			}
			for _, k := range keys {
				app.Infof("%s\t%s\t%s\t%s", k.ID, k.Fingerprint, k.Status, k.ExpiresAt.Format(time.RFC3339))
			}
			return nil
		},
	}
}

func identityKeyAuthorized(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "authorized <key-id>",
		Args:  cobra.ExactArgs(1),
		Short: "Print the authorized_keys line for a key",
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openIdentityStore(app)
			if err != nil {
				return err
			}
			key, ok, err := db.KeyByID(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("key %q not found", args[0])
			}
			if key.PublicKey == "" {
				return fmt.Errorf("key %q has no stored public key", args[0])
			}
			app.Infof(`environment="MENINA_KEY_FINGERPRINT=%s",command="/usr/local/bin/menina ssh-forced" %s`, key.Fingerprint, strings.TrimSpace(key.PublicKey))
			return nil
		},
	}
}

func identityKeyRevoke(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <key-id>",
		Args:  cobra.ExactArgs(1),
		Short: "Revoke one key",
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openIdentityStore(app)
			if err != nil {
				return err
			}
			if err := db.RevokeKey(cmd.Context(), args[0]); err != nil {
				return err
			}
			app.Infof("Revoked key %s", args[0])
			return nil
		},
	}
}

func identityKeyRotate(app *App) *cobra.Command {
	var fingerprint, publicKey, comment, ttl string
	var keepOld bool
	cmd := &cobra.Command{
		Use:   "rotate <key-id>",
		Args:  cobra.ExactArgs(1),
		Short: "Replace one key with a new key",
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openIdentityStore(app)
			if err != nil {
				return err
			}
			spec, err := keySpec(fingerprint, publicKey, comment, ttl)
			if err != nil {
				return err
			}
			key, err := db.RotateKey(cmd.Context(), args[0], spec, !keepOld)
			if err != nil {
				return err
			}
			if app.Opts.JSON {
				return app.PrintJSON(key)
			}
			app.Infof("Rotated key %s to %s (%s)", args[0], key.Fingerprint, key.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&fingerprint, "fingerprint", "", "new key fingerprint")
	cmd.Flags().StringVar(&publicKey, "public-key", "", "authorized_keys line or file path")
	cmd.Flags().StringVar(&comment, "comment", "", "key comment")
	cmd.Flags().StringVar(&ttl, "ttl", "", "optional key lifetime, for example 1h")
	cmd.Flags().BoolVar(&keepOld, "keep-old", false, "keep the old key active")
	return cmd
}

func identityEnrollCreate(app *App) *cobra.Command {
	var ttl string
	cmd := &cobra.Command{
		Use:   "create <profile-id>",
		Args:  cobra.ExactArgs(1),
		Short: "Create a one-time enrollment token",
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openIdentityStore(app)
			if err != nil {
				return err
			}
			duration := identity.DefaultEnrollmentTTL
			if ttl != "" {
				duration, err = time.ParseDuration(ttl)
				if err != nil {
					return err
				}
			}
			plaintext, token, err := identity.NewEnrollmentToken(args[0], duration)
			if err != nil {
				return err
			}
			if _, err := db.CreateEnrollmentToken(cmd.Context(), token); err != nil {
				return err
			}
			if app.Opts.JSON {
				return app.PrintJSON(map[string]any{"token": plaintext, "expiresAt": token.ExpiresAt})
			}
			app.Infof("Enrollment token (shown once): %s", plaintext)
			app.Infof("Expires: %s", token.ExpiresAt.Format(time.RFC3339))
			return nil
		},
	}
	cmd.Flags().StringVar(&ttl, "ttl", "", "token lifetime, default 15m")
	return cmd
}

func identityEnrollConsume(app *App) *cobra.Command {
	var fingerprint, publicKey, comment, ttl string
	cmd := &cobra.Command{
		Use:   "consume <token>",
		Args:  cobra.ExactArgs(1),
		Short: "Consume an enrollment token and attach a key",
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openIdentityStore(app)
			if err != nil {
				return err
			}
			token := args[0]
			if token == "-" {
				data, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return err
				}
				token = strings.TrimSpace(string(data))
			}
			spec, err := keySpec(fingerprint, publicKey, comment, ttl)
			if err != nil {
				return err
			}
			profile, key, err := db.ConsumeEnrollmentToken(cmd.Context(), identity.HashEnrollmentToken(token), time.Now().UTC(), spec)
			if err != nil {
				return err
			}
			if app.Opts.JSON {
				return app.PrintJSON(map[string]any{"profile": profile, "key": key})
			}
			app.Infof("Enrolled key %s for profile %s", key.Fingerprint, profile.Name)
			return nil
		},
	}
	cmd.Flags().StringVar(&fingerprint, "fingerprint", "", "key fingerprint")
	cmd.Flags().StringVar(&publicKey, "public-key", "", "authorized_keys line or file path")
	cmd.Flags().StringVar(&comment, "comment", "", "key comment")
	cmd.Flags().StringVar(&ttl, "ttl", "", "optional key lifetime")
	return cmd
}

func openIdentityStore(app *App) (contract.Store, error) {
	cfg, err := app.LoadConfig()
	if err != nil {
		return nil, err
	}
	return app.OpenDB(cfg)
}

func keySpec(fingerprint, publicKey, comment, ttl string) (identity.Key, error) {
	publicKey = strings.TrimSpace(publicKey)
	if publicKey != "" {
		data, err := os.ReadFile(publicKey)
		if err != nil {
			data = []byte(publicKey)
		}
		fp, parsedComment, err := identity.FingerprintAuthorizedKey(string(data))
		if err != nil {
			return identity.Key{}, err
		}
		fingerprint = fp
		if comment == "" {
			comment = parsedComment
		}
		publicKey = strings.TrimSpace(string(data))
	}
	if fingerprint == "" {
		return identity.Key{}, fmt.Errorf("--fingerprint or --public-key is required")
	}
	key := identity.Key{Fingerprint: fingerprint, PublicKey: publicKey, Comment: comment, Status: identity.StatusActive}
	if ttl != "" {
		d, err := time.ParseDuration(ttl)
		if err != nil {
			return identity.Key{}, err
		}
		if d <= 0 {
			return identity.Key{}, fmt.Errorf("--ttl must be positive")
		}
		key.ExpiresAt = time.Now().UTC().Add(d)
	}
	return key, nil
}
