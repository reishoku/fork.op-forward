package transport

import (
	"fmt"
	"os"
	"path/filepath"
)

const DefaultSocketName = "op-forward.sock"

func SocketPath() (string, error) {
	if p := os.Getenv("OP_FORWARD_SOCKET_PATH"); p != "" {
		return p, nil
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		cacheDir = filepath.Join(home, ".cache")
	}
	if cacheDir == "" {
		return "", fmt.Errorf("cannot resolve cache directory")
	}
	return filepath.Join(cacheDir, "op-forward", DefaultSocketName), nil
}
