package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/labi-le/tidal-syncer/internal/authstore"
	"github.com/labi-le/tidal-syncer/internal/config"
	"github.com/labi-le/tidal-syncer/internal/store"
	"github.com/labi-le/tidal-syncer/pkg/tidal/auth"
)

// defaultClientID is the bundled TIDAL OAuth client_id. It is injected at build
// time via -ldflags and overridden at runtime by config tidal_auth.client_id.
var defaultClientID string

// defaultClientSecret is the bundled TIDAL OAuth client_secret. It is injected
// at build time via -ldflags and overridden by config tidal_auth.client_secret.
var defaultClientSecret string

// newLoginCmd builds the `login` subcommand that runs the TIDAL device
// authorization flow and persists the resulting token through the store.
func newLoginCmd(configPath *string, verbose *bool, lg *zerolog.Logger) *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "Authenticate with TIDAL",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runLogin(cmd.Context(), *configPath, *verbose, *lg)
		},
	}
}

// runLogin loads configuration, opens and migrates the token store, then drives
// the device authorization grant to completion. PollToken persists the token
// through the authstore adapter on success. opts customize the auth client; the
// test suite injects a mock base URL and clock through them.
func runLogin(ctx context.Context, configPath string, verbose bool, lg zerolog.Logger, opts ...auth.Option) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}

	lg, err = leveledLogger(lg, cfg.Log.Level, verbose)
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}

	st, err := store.Open(cfg.Paths.Data)
	if err != nil {
		return fmt.Errorf("login: open store: %w", err)
	}
	defer func() { _ = st.Close() }()

	if err = st.Migrate(ctx); err != nil {
		return fmt.Errorf("login: migrate store: %w", err)
	}

	clientID, clientSecret := resolveCredentials(cfg.TidalAuth)
	client := auth.New(clientID, clientSecret, authstore.New(st), opts...)

	device, err := client.StartDeviceAuth(ctx)
	if err != nil {
		return fmt.Errorf("login: start device authorization: %w", err)
	}

	// Emit the verification link at no level so it surfaces regardless of the
	// configured log level: this is the one line the user must act on.
	lg.Log().
		Str("url", device.VerificationURIComplete).
		Msg("open this link in your browser and approve the login")

	if err = client.PollToken(ctx, device.DeviceCode, device.Interval, device.Expiry); err != nil {
		if errors.Is(err, auth.ErrDeadCredentials) {
			lg.Error().
				Msg("TIDAL rejected the bundled client credentials; set tidal_auth.client_id and tidal_auth.client_secret in your config")
		}

		return fmt.Errorf("login: %w", err)
	}

	lg.Info().Msg("login successful: token stored")

	return nil
}

// resolveCredentials prefers the config-supplied credentials and falls back to
// the build-time ldflag defaults when a field is empty.
func resolveCredentials(cfg config.TidalAuth) (string, string) {
	return cmp.Or(cfg.ClientID, defaultClientID), cmp.Or(cfg.ClientSecret, defaultClientSecret)
}
