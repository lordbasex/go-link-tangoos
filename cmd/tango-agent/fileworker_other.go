//go:build !linux

package main

import (
	"context"
	"log/slog"
	"net"

	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// fdHook atiende las ops que responden con un descriptor (solo Linux).
var fdHook func(ctx context.Context, c *net.UnixConn, req *agentproto.Request, log *slog.Logger) bool

// runFileworkerMode no aplica fuera de Linux.
func runFileworkerMode() bool { return false }
