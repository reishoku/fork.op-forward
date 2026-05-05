package transport

import (
	"fmt"
	"os"
	"path/filepath"
)

const DefaultSocketName = "op-forward.sock"

func SocketPath() (string, error) {
	if p := os.Getenv("OP_FORWARD_SOCKET_PATH"); p != "" {
		return sanitizeSocketPath(p)
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		cacheDir = filepath.Join(home, ".cache")
	}
	if cacheDir == "" {
		return "", fmt.Errorf("cannot resolve cache directory")
	}
	return sanitizeSocketPath(filepath.Join(cacheDir, "op-forward", DefaultSocketName))
}

func sanitizeSocketPath(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("empty socket path")
	}
	clean := filepath.Clean(raw)
	if !filepath.IsAbs(clean) {
		return "", fmt.Errorf("socket path must be absolute: %q", raw)
	}
	return clean, nil
}
