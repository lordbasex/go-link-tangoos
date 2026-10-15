//go:build !linux

package smart

import "github.com/lordbasex/go-link-tangoos/pkg/agentproto"

// Read solo está implementado en Linux.
func Read(devID, dev string) agentproto.DiskSmartResult {
	return agentproto.DiskSmartResult{DevID: devID, Dev: dev, Reason: "SMART solo está disponible en Linux"}
}
