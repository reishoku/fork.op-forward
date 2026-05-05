package cmd

import (
	"path/filepath"
	"testing"
)

func TestProxyTokenPathUsesXDGStateHome(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("OP_FORWARD_TOKEN_FILE", "")
	t.Setenv("OP_FORWARD_TOKEN_DIR", "")
	t.Setenv("XDG_STATE_HOME", stateDir)

	got := proxyTokenPath("refresh.token")
	want := filepath.Join(stateDir, "op-forward", "refresh.token")
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

	if got := proxyTokenPath("access.token"); got != overridePath {
		t.Fatalf("access token path = %q, want %q", got, overridePath)
	}

	got := proxyTokenPath("refresh.token")
	want := filepath.Join(stateDir, "op-forward", "refresh.token")
	if got != want {
		t.Fatalf("refresh token path = %q, want %q", got, want)
	}
}
