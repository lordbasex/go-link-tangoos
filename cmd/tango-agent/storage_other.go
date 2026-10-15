//go:build !linux

package main

import (
	"log/slog"

	"github.com/lordbasex/go-link-tangoos/internal/diskops"
	"github.com/lordbasex/go-link-tangoos/internal/smart"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// Fuera de Linux no hay operaciones de almacenamiento (solo para compilar y testear).
func registerStorage(*agentproto.Registry, smart.Paths, diskops.Env, *asyncJobs, *slog.Logger) {}
