//go:build !linux

package main

import (
	"log/slog"

	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

func registerGluster(*agentproto.Registry, *asyncJobs, *slog.Logger) {}
