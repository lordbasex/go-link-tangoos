package agentclient

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

// fakeAgent atiende con un Registry real; dropFirst cierra la primera conexión
// sin responder para probar la reconexión.
func fakeAgent(t *testing.T, dropFirst bool) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ac")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "a.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	r := agentproto.NewRegistry()
	agentproto.Register(r, agentproto.OpVersion, nil, func(context.Context, *agentproto.NoArgs) (any, *agentproto.Error) {
		return agentproto.VersionResult{Agent: "fake", Protocol: 1}, nil
	})
	agentproto.Register(r, agentproto.OpDiskSmart, nil, func(_ context.Context, a *agentproto.DiskSmartArgs) (any, *agentproto.Error) {
		if a.DevID == "missing" {
			return nil, agentproto.Errf(agentproto.CodeDeviceNotFound, "no existe")
		}
		return agentproto.DiskSmartResult{DevID: a.DevID, Supported: false, Reason: "virtual"}, nil
	})
	go func() {
		first := true
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			if first && dropFirst {
				first = false
				go func() {
					bufio.NewReader(c).ReadBytes('\n')
					c.Close()
				}()
				continue
			}
			go func(c net.Conn) {
				defer c.Close()
				sc := bufio.NewScanner(c)
				enc := json.NewEncoder(c)
				for sc.Scan() {
					req, perr := agentproto.ParseRequest(sc.Bytes())
					if perr != nil {
						enc.Encode(agentproto.Response{V: 1, Error: perr})
						continue
					}
					enc.Encode(r.Dispatch(context.Background(), req))
				}
			}(c)
		}
	}()
	return sock
}

func TestClient(t *testing.T) {
	c := New(fakeAgent(t, false))
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	v, err := c.Version(ctx)
	if err != nil || v.Agent != "fake" {
		t.Fatalf("version: %v %+v", err, v)
	}
	s, err := c.DiskSmart(ctx, "scsi-0QEMU_QEMU_HARDDISK_TANGO-D1")
	if err != nil || s.Supported || s.Reason != "virtual" {
		t.Fatalf("smart: %v %+v", err, s)
	}
	_, err = c.DiskSmart(ctx, "missing")
	var ae *agentproto.Error
	if !errors.As(err, &ae) || ae.Code != agentproto.CodeDeviceNotFound {
		t.Fatalf("error tipado esperado, got %v", err)
	}
}

func TestClientReconnects(t *testing.T) {
	c := New(fakeAgent(t, true))
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := c.Version(ctx); err != nil {
		t.Fatalf("debe reconectar tras una conexión cortada: %v", err)
	}
}

func TestClientUnavailable(t *testing.T) {
	c := New(filepath.Join(t.TempDir(), "nope.sock"))
	_, err := c.Version(context.Background())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ErrUnavailable esperado, got %v", err)
	}
}
