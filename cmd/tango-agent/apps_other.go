//go:build !linux

package main

import (
	"log/slog"

	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// Fuera de Linux no hay Centro de apps (solo para compilar y testear).
func registerApps(*agentproto.Registry, *asyncJobs, *slog.Logger) {}
