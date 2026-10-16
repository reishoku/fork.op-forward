package cmd

import (
	"flag"
	"os"

	"github.com/reishoku/fork.op-forward/internal/endpoint"
)

// explicitFlags returns the names of the flags the user actually typed, as
// opposed to those holding their defaults.
func explicitFlags(fs *flag.FlagSet) map[string]bool {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return set
}

// endpointArg decides whether a full endpoint string should be used or
// discarded in favor of its shorthand aliases (--host/--port for the proxy,
// --port for the daemon). The endpoint flag's default is read from the
// environment, so without this rule an OP_FORWARD_ADDR exported in a shell
// profile would silently override a --host the user typed on the command
// line. Precedence is: typed endpoint > typed alias > environment endpoint
// > environment alias.
func endpointArg(value string, explicit map[string]bool, aliases ...string) string {
	if explicit["addr"] || explicit["listen"] {
		return value
	}
	for _, alias := range aliases {
		if explicit[alias] {
			return ""
		}
	}
	return value
}

// aliasEnv maps each TCP shorthand flag to the environment variable that
// supplies its default.
var aliasEnv = map[string]string{
	"host": endpoint.EnvHost,
	"port": endpoint.EnvPort,
}

// tcpShorthand reports whether the TCP shorthand was requested instead of
// the default Unix socket: one of the alias flags was typed, or the
// environment variable behind it is set. It only matters when endpointArg
// returned no full endpoint.
func tcpShorthand(explicit map[string]bool, aliases ...string) bool {
	for _, alias := range aliases {
		if explicit[alias] || os.Getenv(aliasEnv[alias]) != "" {
			return true
		}
	}
	return false
}

// getProbeTimeoutMs returns how long the proxy waits for the daemon to accept
// a connection before declaring the tunnel down and letting the shim fall
// back to the local op binary. Controlled by OP_FORWARD_PROBE_TIMEOUT_MS. A
// zero value would mean "no timeout" to the dialer and defeat the fallback,
// so non-positive values revert to the default.
func getProbeTimeoutMs() int {
	return endpoint.IntFromEnv("OP_FORWARD_PROBE_TIMEOUT_MS", 500, 1)
}

// getProxyTimeout returns the per-request execution timeout in milliseconds,
// controlled by OP_FORWARD_FETCH_TIMEOUT_MS.
func getProxyTimeout() int {
	return endpoint.IntFromEnv("OP_FORWARD_FETCH_TIMEOUT_MS", 60000, 1)
}
