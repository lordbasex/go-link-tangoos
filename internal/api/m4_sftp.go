package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// SFTP (protocol/README.md §12): servidor propio en Go dentro de tangod (puerto 2222 por
// defecto), sin tocar sshd ni dar shell. Mismos usuarios, contraseñas y permisos que el
// resto (file-worker por usuario).

type sftpServer struct {
	a    *API
	mu   sync.Mutex
	ln   net.Listener
	port int
}

func hostKey(stateDir string) (ssh.Signer, error) {
	p := filepath.Join(stateDir, "sftp_host_ed25519")
	if b, err := os.ReadFile(p); err == nil {
		return ssh.ParsePrivateKey(b)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	blk, err := ssh.MarshalPrivateKey(priv, "tangoos sftp")
	if err != nil {
		return nil, err
	}
	b := pem.EncodeToMemory(blk)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey(b)
}

// start abre el puerto (idempotente); stop lo cierra.
func (s *sftpServer) start(port int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln != nil && s.port == port {
		return nil
	}
	if s.ln != nil {
		_ = s.ln.Close()
		s.ln = nil
	}
	signer, err := hostKey(s.a.d.StateDir)
	if err != nil {
		return err
	}
	cfg := &ssh.ServerConfig{
		ServerVersion: "SSH-2.0-TangoOS-SFTP",
		MaxAuthTries:  3,
		PasswordCallback: func(c ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			ip, _, _ := net.SplitHostPort(c.RemoteAddr().String())
			if _, err := s.a.d.Auth.VerifyService(c.User(), string(pw), ip); err != nil {
				return nil, errors.New("acceso denegado")
			}
			return &ssh.Permissions{Extensions: map[string]string{"user": c.User()}}, nil
		},
	}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return err
	}
	s.ln, s.port = ln, port
	go s.serve(ln, cfg)
	slog.Info("SFTP escuchando", "port", port)
	return nil
}

func (s *sftpServer) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln != nil {
		_ = s.ln.Close()
		s.ln = nil
	}
}

func (s *sftpServer) serve(ln net.Listener, cfg *ssh.ServerConfig) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go s.handle(c, cfg)
	}
}

func (s *sftpServer) handle(nc net.Conn, cfg *ssh.ServerConfig) {
	_ = nc.SetDeadline(time.Now().Add(30 * time.Second))
	conn, chans, reqs, err := ssh.NewServerConn(nc, cfg)
	if err != nil {
		nc.Close()
		return
	}
	_ = nc.SetDeadline(time.Time{})
	defer conn.Close()
	go ssh.DiscardRequests(reqs)
	user := conn.Permissions.Extensions["user"]
	ip, _, _ := net.SplitHostPort(conn.RemoteAddr().String())
	if s.a.m4 != nil {
		s.a.m4.audit(user, ip, "sftp", "sftp.login", "", "ok")
	}
	for nch := range chans {
		if nch.ChannelType() != "session" {
			_ = nch.Reject(ssh.UnknownChannelType, "solo SFTP")
			continue
		}
		ch, in, err := nch.Accept()
		if err != nil {
			continue
		}
		go func() {
			for req := range in {
				ok := req.Type == "subsystem" && len(req.Payload) >= 4 && string(req.Payload[4:]) == "sftp"
				_ = req.Reply(ok, nil)
				if !ok {
					continue
				}
				u := s.a.files.newUserFS(user, ip, "sftp")
				h := sftpHandlers{u: u, audit: func(m, t string) {
					if s.a.m4 != nil {
						s.a.m4.audit(user, ip, "sftp", m, t, "ok")
					}
				}}
				srv := sftp.NewRequestServer(ch, sftp.Handlers{FileGet: h, FilePut: h, FileCmd: h, FileList: h})
				_ = srv.Serve()
				_ = srv.Close()
				return
			}
		}()
	}
}

type sftpHandlers struct {
	u     *userFS
	audit func(method, target string)
}

func bg() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 60*time.Second)
}

func (h sftpHandlers) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	ctx, cancel := bg()
	defer cancel()
	return h.u.OpenRead(ctx, r.Filepath)
}

func (h sftpHandlers) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	ctx, cancel := bg()
	defer cancel()
	w, err := h.u.Create(ctx, r.Filepath)
	if err != nil {
		return nil, err
	}
	h.audit("sftp.put", r.Filepath)
	return w, nil
}

func (h sftpHandlers) Filecmd(r *sftp.Request) error {
	ctx, cancel := bg()
	defer cancel()
	switch r.Method {
	case "Setstat":
		return nil
	case "Rename", "PosixRename":
		h.audit("sftp.rename", r.Filepath+" → "+r.Target)
		return h.u.Rename(ctx, r.Filepath, r.Target)
	case "Rmdir", "Remove":
		h.audit("sftp.remove", r.Filepath)
		return h.u.Remove(ctx, r.Filepath)
	case "Mkdir":
		h.audit("sftp.mkdir", r.Filepath)
		return h.u.Mkdir(ctx, r.Filepath)
	}
	return sftp.ErrSSHFxOpUnsupported
}

type listerAt []fs.FileInfo

func (l listerAt) ListAt(out []fs.FileInfo, off int64) (int, error) {
	if off >= int64(len(l)) {
		return 0, io.EOF
	}
	n := copy(out, l[off:])
	if n < len(out) {
		return n, io.EOF
	}
	return n, nil
}

func (h sftpHandlers) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	ctx, cancel := bg()
	defer cancel()
	switch r.Method {
	case "List":
		l, err := h.u.ReadDir(ctx, r.Filepath)
		if err != nil {
			return nil, err
		}
		out := make(listerAt, len(l))
		for i, x := range l {
			out[i] = x
		}
		return out, nil
	case "Stat":
		i, err := h.u.Stat(ctx, r.Filepath)
		if err != nil {
			return nil, err
		}
		return listerAt{i}, nil
	}
	return nil, sftp.ErrSSHFxOpUnsupported
}
