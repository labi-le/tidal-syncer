package main

import (
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

	client := auth.New(cfg.TidalAuth.ClientID, cfg.TidalAuth.ClientSecret, authstore.New(st), opts...)

	err = driveDeviceAuth(ctx, client, func(device auth.DeviceAuth) {
		// Emit the verification link at no level so it surfaces regardless of the
		// configured log level: this is the one line the user must act on.
		lg.Log().
			Str("url", fmt.Sprintf("https://%s", device.VerificationURIComplete)).
			Msg("open this link in your browser and approve the login")
	})
	if err != nil {
		if errors.Is(err, auth.ErrDeadCredentials) {
			lg.Error().
				Msg("TIDAL rejected the client credentials; set tidal_auth.client_id and tidal_auth.client_secret in your config")
		}

		return fmt.Errorf("login: %w", err)
	}

	lg.Info().Msg("login successful: token stored")

	return nil
}

// driveDeviceAuth runs one TIDAL device-authorization grant to completion. It
// starts the grant, hands the resulting [auth.DeviceAuth] to onLink so the
// caller can publish the verification link, then polls the token endpoint until
// the user approves the login, the device code expires, or ctx is done.
// [auth.Client.PollToken] persists the refreshed token through the client's
// store on success. The login command owns this helper so the start-link-poll
// sequence lives in exactly one place.
func driveDeviceAuth(ctx context.Context, authClient *auth.Client, onLink func(auth.DeviceAuth)) error {
	device, err := authClient.StartDeviceAuth(ctx)
	if err != nil {
		return fmt.Errorf("start device authorization: %w", err)
	}

	onLink(device)

	if err = authClient.PollToken(ctx, device.DeviceCode, device.Interval, device.Expiry); err != nil {
		return fmt.Errorf("poll device token: %w", err)
	}

	return nil
}
