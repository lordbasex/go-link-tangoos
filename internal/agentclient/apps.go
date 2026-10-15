package agentclient

import (
	"context"

	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// ---- Centro de apps (protocol/README.md §9) -----------------------------------------

// DockerStatus dice si Docker está instalado y corriendo.
func (c *Client) DockerStatus(ctx context.Context) (*agentproto.DockerStatus, error) {
	var r agentproto.DockerStatus
	if err := c.Call(ctx, agentproto.OpDockerStatus, nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// DockerInstall arranca la instalación de Docker (asíncrona: seguir con AppJob).
func (c *Client) DockerInstall(ctx context.Context, opID string) error {
	return c.CallOnce(ctx, agentproto.OpDockerInstall, agentproto.DockerInstallArgs{OpID: opID}, nil)
}

// AppUp instala/actualiza una app y la deja corriendo (asíncrona).
func (c *Client) AppUp(ctx context.Context, a agentproto.AppUpArgs) error {
	return c.CallOnce(ctx, agentproto.OpAppUp, a, nil)
}

// AppAction inicia, detiene o reinicia una app (asíncrona).
func (c *Client) AppAction(ctx context.Context, a agentproto.AppActionArgs) error {
	return c.CallOnce(ctx, agentproto.OpAppAction, a, nil)
}

// AppUninstall desinstala una app (asíncrona).
func (c *Client) AppUninstall(ctx context.Context, a agentproto.AppUninstallArgs) error {
	return c.CallOnce(ctx, agentproto.OpAppUninstall, a, nil)
}

// AppLogs devuelve las últimas líneas de log de una app.
func (c *Client) AppLogs(ctx context.Context, id string, tail int) ([]string, error) {
	var r agentproto.AppLogsResult
	if err := c.Call(ctx, agentproto.OpAppLogs, agentproto.AppLogsArgs{ID: id, Tail: tail}, &r); err != nil {
		return nil, err
	}
	return r.Lines, nil
}

// AppStats devuelve el estado real de las apps según Docker.
func (c *Client) AppStats(ctx context.Context, ids []string) ([]agentproto.AppStat, error) {
	var r agentproto.AppStatsResult
	if err := c.Call(ctx, agentproto.OpAppStats, agentproto.AppStatsArgs{IDs: ids}, &r); err != nil {
		return nil, err
	}
	return r.Apps, nil
}

// AppJob consulta una operación asíncrona del Centro de apps.
func (c *Client) AppJob(ctx context.Context, opID string) (*JobStatus, error) {
	var r JobStatus
	if err := c.Call(ctx, agentproto.OpAppJob, agentproto.AppJobArgs{OpID: opID}, &r); err != nil {
		return nil, err
	}
	return &r, nil
}
