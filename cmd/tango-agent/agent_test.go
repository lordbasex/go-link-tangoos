package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/smart"
	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

func startServer(t *testing.T, allowed map[uint32]bool) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ta") // ruta corta: los sockets Unix tienen límite de ~104 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "a.sock")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	j, _ := openJournal(filepath.Join(dir, "j"))
	paths := smart.Paths{ByID: filepath.Join(dir, "by-id"), SysBlock: filepath.Join(dir, "sys")}
	s := &server{reg: newRegistry(paths), allowed: allowed, log: slog.New(slog.NewTextHandler(io.Discard, nil)), journal: j}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = s.serve(ctx, ln); close(done) }()
	t.Cleanup(func() { cancel(); ln.Close(); <-done })
	return sock
}

func call(t *testing.T, rw *bufio.ReadWriter, line string) agentproto.Response {
	t.Helper()
	if _, err := rw.WriteString(line + "\n"); err != nil {
		t.Fatal(err)
	}
	rw.Flush()
	b, err := rw.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var r agentproto.Response
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestServer(t *testing.T) {
	sock := startServer(t, map[uint32]bool{uint32(os.Getuid()): true})
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	rw := bufio.NewReadWriter(bufio.NewReader(c), bufio.NewWriter(c))

	r := call(t, rw, `{"v":1,"id":1,"op":"agent.version"}`)
	if !r.OK || r.ID != 1 {
		t.Fatalf("version: %+v", r)
	}
	r = call(t, rw, `{"v":1,"id":2,"op":"exec","args":{"cmd":"id"}}`)
	if r.OK || r.Error.Code != agentproto.CodeUnknownOp {
		t.Fatalf("op desconocida: %+v", r)
	}
	r = call(t, rw, `{"v":1,"id":3,"op":"disk.smart","args":{"dev_id":"../../dev/sda"}}`)
	if r.OK || r.Error.Code != agentproto.CodeInvalidArgs {
		t.Fatalf("path traversal: %+v", r)
	}
	r = call(t, rw, `{"v":1,"id":4,"op":"disk.smart","args":{"dev_id":"wwn-0xnope"}}`)
	if r.OK || r.Error.Code != agentproto.CodeDeviceNotFound {
		t.Fatalf("inexistente: %+v", r)
	}
	r = call(t, rw, `{nope`)
	if r.OK || r.Error.Code != agentproto.CodeBadRequest {
		t.Fatalf("basura: %+v", r)
	}
	// Rate limit: la ráfaga máxima es 40.
	limited := false
	for i := 0; i < 60; i++ {
		if r := call(t, rw, `{"v":1,"id":9,"op":"agent.version"}`); r.Error != nil && r.Error.Code == agentproto.CodeRateLimited {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("el rate limit no actuó")
	}
}

func TestServerRejectsPeer(t *testing.T) {
	sock := startServer(t, map[uint32]bool{4_000_000_000: true})
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(2 * time.Second))
	c.Write([]byte(`{"v":1,"id":1,"op":"agent.version"}` + "\n"))
	_, err = bufio.NewReader(c).ReadBytes('\n')
	if err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("la conexión de un uid no autorizado debe cerrarse, err=%v", err)
	}
}

func TestJournal(t *testing.T) {
	j, err := openJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := j.begin("j_1", "storage.volume.create", json.RawMessage(`{"name":"volume2"}`)); err != nil {
		t.Fatal(err)
	}
	if err := j.begin("j_1", "storage.volume.create", nil); err == nil {
		t.Fatal("un op_id repetido debe rechazarse")
	}
	if err := j.begin("../x", "op", nil); err == nil {
		t.Fatal("op_id inválido")
	}
	if err := j.begin("j_2", "storage.scrub", nil); err != nil {
		t.Fatal(err)
	}
	if err := j.end("j_2", nil); err != nil {
		t.Fatal(err)
	}
	n, err := j.recover()
	if err != nil || n != 1 {
		t.Fatalf("recover = %d, %v", n, err)
	}
	e, _ := j.read("j_1")
	if e.State != "interrupted" || e.Ended == nil {
		t.Fatalf("j_1 = %+v", e)
	}
	e, _ = j.read("j_2")
	if e.State != "succeeded" {
		t.Fatalf("j_2 = %+v", e)
	}
}

func TestValidateVolumeCreate(t *testing.T) {
	fp := strings.Repeat("a", 64)
	ok := agentproto.VolumeCreateArgs{OpID: "j_abc123", Name: "volume1", Profile: "raid1",
		Disks: []agentproto.VolumeDisk{{DevID: "scsi-1", Fingerprint: fp}, {DevID: "scsi-2", Fingerprint: fp}}}
	if e := validateVolumeCreate(&ok); e != nil {
		t.Fatalf("válido rechazado: %v", e)
	}
	bad := []func(a *agentproto.VolumeCreateArgs){
		func(a *agentproto.VolumeCreateArgs) { a.Name = "etc" },
		func(a *agentproto.VolumeCreateArgs) { a.Name = "../x" },
		func(a *agentproto.VolumeCreateArgs) { a.Profile = "raid5" },
		func(a *agentproto.VolumeCreateArgs) { a.Profile = "basic" }, // basic con 2 discos
		func(a *agentproto.VolumeCreateArgs) { a.Disks[1].DevID = "scsi-1" },
		func(a *agentproto.VolumeCreateArgs) { a.Disks[0].Fingerprint = "corta" },
		func(a *agentproto.VolumeCreateArgs) { a.OpID = "x/../y" },
	}
	for i, mut := range bad {
		a := ok
		a.Disks = append([]agentproto.VolumeDisk(nil), ok.Disks...)
		mut(&a)
		if e := validateVolumeCreate(&a); e == nil || e.Code != agentproto.CodeInvalidArgs {
			t.Errorf("caso %d aceptado: %+v", i, a)
		}
	}
}

func TestValidateSamba(t *testing.T) {
	if e := validateSamba(&agentproto.SambaShareArgs{Name: "Familia", Path: "/volume1/@shares/Familia"}); e != nil {
		t.Fatal(e)
	}
	for _, a := range []agentproto.SambaShareArgs{
		{Name: "Familia", Path: "/etc/@shares/Familia"},
		{Name: "Familia", Path: "/volume1/@shares/Otra"},
		{Name: "Familia", Path: "/volume1/@shares/Familia/../../etc"},
		{Name: "global", Path: "/volume1/@shares/global"},
		{Name: "Familia", Path: "/volume1/@shares/Familia", Comment: strings.Repeat("x", 300)},
	} {
		if validateSamba(&a) == nil {
			t.Errorf("aceptado: %+v", a)
		}
	}
}

func TestStorageOpsRejectUnknownFields(t *testing.T) {
	r := agentproto.NewRegistry()
	agentproto.Register(r, agentproto.OpVolumeCreate, validateVolumeCreate, func(context.Context, *agentproto.VolumeCreateArgs) (any, *agentproto.Error) {
		t.Fatal("no debía ejecutarse")
		return nil, nil
	})
	resp := r.Dispatch(context.Background(), &agentproto.Request{V: 1, ID: 1, Op: agentproto.OpVolumeCreate,
		Args: []byte(`{"op_id":"j_1","name":"volume1","profile":"basic","disks":[],"force":true}`)})
	if resp.OK || resp.Error.Code != agentproto.CodeInvalidArgs {
		t.Fatalf("%+v", resp)
	}
}
