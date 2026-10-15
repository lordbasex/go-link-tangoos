//go:build !linux

package main

import "github.com/lordbasex/go-link-tangoos/pkg/agentproto"

func registerUPS(*agentproto.Registry, *asyncJobs) {}
