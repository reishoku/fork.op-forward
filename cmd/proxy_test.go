package cmd

import (
	"path/filepath"
	"testing"

	"github.com/reishoku/fork.op-forward/internal/auth"
)

func TestProxyTokenPathUsesXDGStateHome(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("OP_FORWARD_TOKEN_FILE", "")
	t.Setenv("OP_FORWARD_TOKEN_DIR", "")
	t.Setenv("XDG_STATE_HOME", stateDir)

	got := proxyTokenPath(auth.RefreshTokenFile)
	want, err := auth.RefreshTokenPath()
	if err != nil {
		t.Fatalf("RefreshTokenPath() error: %v", err)
	}
	if got != want {
		t.Fatalf("proxyTokenPath() = %q, want %q", got, want)
	}
}

func TestProxyTokenPathAccessTokenFileOverrideOnlyAppliesToAccess(t *testing.T) {
	stateDir := t.TempDir()
	overridePath := filepath.Join(t.TempDir(), "custom-access.token")
	t.Setenv("OP_FORWARD_TOKEN_FILE", overridePath)
	t.Setenv("OP_FORWARD_TOKEN_DIR", "")
	t.Setenv("XDG_STATE_HOME", stateDir)

	if got := proxyTokenPath(auth.AccessTokenFile); got != overridePath {
		t.Fatalf("access token path = %q, want %q", got, overridePath)
	}

	got := proxyTokenPath(auth.RefreshTokenFile)
	want := filepath.Join(stateDir, "op-forward", auth.RefreshTokenFile)
	if got != want {
		t.Fatalf("refresh token path = %q, want %q", got, want)
	}
}
