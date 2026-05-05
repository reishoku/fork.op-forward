//go:build !linux && !darwin

package transport

import "net"

func PeerUID(conn *net.UnixConn) (int, error) { return -1, nil }
