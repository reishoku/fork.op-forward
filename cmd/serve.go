package cmd

import (
	"flag"
	"fmt"
	"os"

	"github.com/reishoku/fork.op-forward/internal/transport"

	"github.com/reishoku/fork.op-forward/internal/auth"
	"github.com/reishoku/fork.op-forward/internal/daemon"
)

func runServe() error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	fs.Parse(os.Args[2:])

	// Migrate legacy session.token → refresh.token if upgrading from the
	// single-token system.
	if err := auth.MigrateLegacyToken(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: legacy token migration: %v\n", err)
	}

	// Load or generate the refresh token (30-day TTL, persists across restarts).
	refreshToken, isNewRefresh, err := auth.LoadOrGenerateRefresh()
	if err != nil {
		return fmt.Errorf("refresh token setup: %w", err)
	}
	refreshPath, _ := auth.RefreshTokenPath()
	if isNewRefresh {
		fmt.Printf("Refresh token generated (no valid existing token found)\n")
	} else {
		fmt.Printf("Refresh token reused (expires %s)\n", refreshToken.Expires.Format("2006-01-02T15:04:05-07:00"))
	}
	fmt.Printf("Refresh token at: %s\n", refreshPath)

	// Always generate a fresh access token on startup (1-hour TTL).
	accessToken, err := auth.GenerateAccess()
	if err != nil {
		return fmt.Errorf("access token setup: %w", err)
	}
	accessPath, _ := auth.AccessTokenPath()
	if err := auth.SaveToPath(accessToken, accessPath); err != nil {
		return fmt.Errorf("saving access token: %w", err)
	}
	fmt.Printf("Access token written to: %s (expires %s)\n",
		accessPath, accessToken.Expires.Format("2006-01-02T15:04:05-07:00"))

	// Write the access token to the legacy session.token path so that older
	// proxy clients that read session.token continue to work until upgraded.
	if legacyPath, err := auth.LegacyTokenPath(); err == nil {
		auth.SaveToPath(accessToken, legacyPath)
	}

	socketPath, err := transport.SocketPath()
	if err != nil {
		return fmt.Errorf("socket path: %w", err)
	}
	fmt.Printf("Starting daemon on unix://%s\n", socketPath)

	server := daemon.New(accessToken, refreshToken, socketPath, Version)
	return server.Start()
}
