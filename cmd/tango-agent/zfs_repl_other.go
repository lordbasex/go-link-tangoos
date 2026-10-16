//go:build !linux

package main

import (
	"context"
	"log/slog"
	"net"

	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

func replHook(context.Context, *net.UnixConn, *agentproto.Request, *slog.Logger) bool { return false }
