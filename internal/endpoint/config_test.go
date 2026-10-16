package endpoint

import (
	"os"
	"path/filepath"
	"testing"
)

func TestForDialUsesHostAndPortWhenAddrEmpty(t *testing.T) {
	ep, err := ForDial("", "host.docker.internal", 20000, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := ep.String(); got != "tcp://host.docker.internal:20000" {
		t.Fatalf("ForDial() = %q", got)
	}
}

func TestForDialAddrWins(t *testing.T) {
	ep, err := ForDial("unix:///run/op-forward.sock", "host.docker.internal", 20000, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := ep.String(); got != "unix:///run/op-forward.sock" {
		t.Fatalf("ForDial() = %q", got)
	}
}

func TestForDialRejectsMalformedAddr(t *testing.T) {
	if _, err := ForDial("127.0.0.1:18340", "h", 1, true); err == nil {
		t.Fatal("ForDial() accepted an address without a scheme")
	}
}

func TestForListenTCPShorthandBindsLoopback(t *testing.T) {
	ep, err := ForListen("", 20000, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := ep.String(); got != "tcp://127.0.0.1:20000" {
		t.Fatalf("ForListen() = %q", got)
	}
}

func TestForListenExplicitWins(t *testing.T) {
	ep, err := ForListen("unix:///run/op-forward.sock", 20000, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := ep.String(); got != "unix:///run/op-forward.sock" {
		t.Fatalf("ForListen() = %q", got)
	}
}

// Without a full endpoint or the TCP shorthand, both sides must resolve the
// same default Unix socket, or the proxy would dial a path the daemon never
// bound.
func TestForDialAndForListenDefaultToUnixSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "op.sock")
	t.Setenv(EnvSocketPath, path)

	dial, err := ForDial("", DefaultHost, DefaultPort, false)
	if err != nil {
		t.Fatal(err)
	}
	listen, err := ForListen("", DefaultPort, false)
	if err != nil {
		t.Fatal(err)
	}
	want := "unix://" + path
	if dial.String() != want || listen.String() != want {
		t.Fatalf("ForDial() = %q, ForListen() = %q, want both %q", dial, listen, want)
	}
}

func TestDefaultSocketPathPrecedence(t *testing.T) {
	runtimeDir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", "")

	t.Setenv(EnvSocketPath, "/custom/dir/../op.sock")
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	if got, err := DefaultSocketPath(); err != nil || got != "/custom/op.sock" {
		t.Fatalf("with %s set: DefaultSocketPath() = %q, %v; want the cleaned override", EnvSocketPath, got, err)
	}

	t.Setenv(EnvSocketPath, "")
	if got, err := DefaultSocketPath(); err != nil || got != filepath.Join(runtimeDir, DefaultSocketName) {
		t.Fatalf("with XDG_RUNTIME_DIR set: DefaultSocketPath() = %q, %v", got, err)
	}

	// A relative XDG_RUNTIME_DIR is invalid per the XDG specification and
	// must be ignored in favor of the cache directory.
	for _, runtime := range []string{"", "relative/run"} {
		t.Setenv("XDG_RUNTIME_DIR", runtime)
		cacheDir, err := os.UserCacheDir()
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(cacheDir, "op-forward", DefaultSocketName)
		if got, err := DefaultSocketPath(); err != nil || got != want {
			t.Fatalf("with XDG_RUNTIME_DIR=%q: DefaultSocketPath() = %q, %v; want %q", runtime, got, err, want)
		}
	}
}

func TestDefaultSocketPathRejectsRelativeOverride(t *testing.T) {
	t.Setenv(EnvSocketPath, "op.sock")
	if got, err := DefaultSocketPath(); err == nil {
		t.Fatalf("DefaultSocketPath() = %q, want an error for a relative %s", got, EnvSocketPath)
	}
	if _, err := ForListen("", DefaultPort, false); err == nil {
		t.Fatal("ForListen() must surface the invalid default socket path")
	}
}

func TestIntFromEnv(t *testing.T) {
	const key = "OP_FORWARD_TEST_INT"
	cases := map[string]int{"": 7, "12": 12, "garbage": 7, "0": 7, "-3": 7, "1": 1}
	for raw, want := range cases {
		t.Setenv(key, raw)
		if got := IntFromEnv(key, 7, 1); got != want {
			t.Errorf("IntFromEnv(%q) = %d, want %d", raw, got, want)
		}
	}
}

func TestPortFromEnvRejectsZeroAndGarbage(t *testing.T) {
	for _, raw := range []string{"not-a-port", "0", "-1"} {
		t.Setenv(EnvPort, raw)
		if got := PortFromEnv(); got != DefaultPort {
			t.Errorf("PortFromEnv() with %q = %d, want %d", raw, got, DefaultPort)
		}
	}
}

func TestHostFromEnv(t *testing.T) {
	t.Setenv(EnvHost, "")
	if got := HostFromEnv(); got != DefaultHost {
		t.Fatalf("HostFromEnv() default = %q", got)
	}
	t.Setenv(EnvHost, "host.docker.internal")
	if got := HostFromEnv(); got != "host.docker.internal" {
		t.Fatalf("HostFromEnv() = %q", got)
	}
}
