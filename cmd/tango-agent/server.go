package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

type server struct {
	reg     *agentproto.Registry
	allowed map[uint32]bool
	log     *slog.Logger
	journal *journal
	wg      sync.WaitGroup
}

func (s *server) serve(ctx context.Context, ln *net.UnixListener) error {
	defer s.wg.Wait()
	for {
		c, err := ln.AcceptUnix()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		uid, pid, err := peerCred(c)
		if err != nil || !s.allowed[uid] {
			s.log.Warn("conexión rechazada", "uid", uid, "pid", pid, "err", err)
			_ = c.Close()
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handle(ctx, c, uid, pid)
		}()
	}
}

// bucket es un rate limiter de token bucket por conexión.
type bucket struct {
	tokens, max, rate float64
	last              time.Time
}

func (b *bucket) allow(now time.Time) bool {
	b.tokens = min(b.max, b.tokens+now.Sub(b.last).Seconds()*b.rate)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (s *server) handle(ctx context.Context, c *net.UnixConn, uid uint32, pid int32) {
	defer c.Close()
	log := s.log.With("peer_uid", uid, "peer_pid", pid)
	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 4096), agentproto.MaxLine)
	enc := json.NewEncoder(c)
	lim := &bucket{tokens: 40, max: 40, rate: 20, last: time.Now()}
	for sc.Scan() {
		line := sc.Bytes()
		req, perr := agentproto.ParseRequest(line)
		var resp agentproto.Response
		switch {
		case perr != nil:
			resp = agentproto.Response{V: agentproto.Version, Error: perr}
			if req != nil {
				resp.ID = req.ID
			}
		case !lim.allow(time.Now()):
			resp = agentproto.Response{V: agentproto.Version, ID: req.ID, Error: agentproto.Errf(agentproto.CodeRateLimited, "demasiados pedidos")}
		default:
			if fdHook != nil && fdHook(ctx, c, req, log) {
				continue // ops que responden con un descriptor (files.worker)
			}
			start := time.Now()
			octx, cancel := context.WithTimeout(ctx, opTimeout)
			resp = s.reg.Dispatch(octx, req)
			cancel()
			attrs := []any{"op", req.Op, "id", req.ID, "ok", resp.OK, "ms", time.Since(start).Milliseconds()}
			if resp.Error != nil {
				attrs = append(attrs, "code", resp.Error.Code)
			}
			log.Info("op", attrs...)
		}
		if err := enc.Encode(resp); err != nil {
			return
		}
	}
	if err := sc.Err(); err != nil && !errors.Is(err, net.ErrClosed) {
		log.Warn("conexión cerrada por input inválido", "err", err)
	}
}
