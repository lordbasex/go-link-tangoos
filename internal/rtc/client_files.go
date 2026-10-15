package rtc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/lordbasex/go-link-tangoos/internal/fileworker"
)

// Transferencias del lado cliente (tangoctl, tests; base de tango-desk).

type clientFiles struct {
	mu    sync.Mutex
	dcs   map[string]*webrtc.DataChannel
	low   map[string]chan struct{}
	open  map[string]chan struct{}
	recv  map[uint32]*download
	early map[uint32][][]byte // frames de xfers que todavía no conocemos
}

type download struct {
	f     *os.File
	size  int64
	got   int64
	done  chan struct{}
	err   error
	once  sync.Once
	h     hashWriter
	nextO int64
}

type hashWriter interface {
	Write([]byte) (int, error)
	Sum([]byte) []byte
}

func (c *Client) fs() *clientFiles {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.files == nil {
		c.files = &clientFiles{dcs: map[string]*webrtc.DataChannel{}, low: map[string]chan struct{}{}, open: map[string]chan struct{}{}, recv: map[uint32]*download{}, early: map[uint32][][]byte{}}
	}
	return c.files
}

func (cf *clientFiles) openCh(label string) chan struct{} {
	ch := cf.open[label]
	if ch == nil {
		ch = make(chan struct{})
		cf.open[label] = ch
	}
	return ch
}

func (c *Client) addFileChannel(dc *webrtc.DataChannel) {
	if !strings.HasPrefix(dc.Label(), "files.") {
		return
	}
	cf := c.fs()
	label := dc.Label()
	cf.mu.Lock()
	cf.dcs[label] = dc
	low := make(chan struct{}, 1)
	cf.low[label] = low
	openCh := cf.openCh(label)
	cf.mu.Unlock()
	dc.SetBufferedAmountLowThreshold(lowWater)
	dc.OnBufferedAmountLow(func() {
		select {
		case low <- struct{}{}:
		default:
		}
	})
	dc.OnOpen(func() { close(openCh) })
	dc.OnMessage(func(m webrtc.DataChannelMessage) {
		if !m.IsString {
			cf.onFrame(m.Data)
		}
	})
}

func (cf *clientFiles) onFrame(b []byte) {
	id, off, data, err := fileworker.DecodeFrame(b)
	if err != nil {
		return
	}
	cf.mu.Lock()
	d := cf.recv[id]
	if d == nil {
		if len(cf.early[id]) < 256 {
			cf.early[id] = append(cf.early[id], append([]byte(nil), b...))
		}
		cf.mu.Unlock()
		return
	}
	cf.mu.Unlock()
	d.write(int64(off), data)
}

func (d *download) write(off int64, data []byte) {
	if _, err := d.f.WriteAt(data, off); err != nil {
		d.finish(err)
		return
	}
	if off == d.nextO {
		d.h.Write(data)
		d.nextO += int64(len(data))
	}
	d.got += int64(len(data))
	if d.got >= d.size {
		d.finish(nil)
	}
}

func (d *download) finish(err error) {
	d.once.Do(func() {
		d.err = err
		close(d.done)
	})
}

func (c *Client) waitFileChannel(ctx context.Context, label string) (*webrtc.DataChannel, chan struct{}, error) {
	cf := c.fs()
	cf.mu.Lock()
	openCh := cf.openCh(label)
	cf.mu.Unlock()
	select {
	case <-openCh:
	case <-ctx.Done():
		return nil, nil, fmt.Errorf("canal %s: %w", label, ctx.Err())
	}
	cf.mu.Lock()
	defer cf.mu.Unlock()
	return cf.dcs[label], cf.low[label], nil
}

// TransferStats resume una transferencia.
type TransferStats struct {
	Bytes   int64         `json:"bytes"`
	Elapsed time.Duration `json:"elapsed"`
	SHA256  string        `json:"sha256"`
	Entry   json.RawMessage
}

// MBps es el throughput en MB/s.
func (t TransferStats) MBps() float64 {
	if t.Elapsed <= 0 {
		return 0
	}
	return float64(t.Bytes) / 1e6 / t.Elapsed.Seconds()
}

// Upload sube local a la carpeta remota remoteDir (ruta virtual).
func (c *Client) Upload(ctx context.Context, local, remoteDir string, overwrite bool) (TransferStats, error) {
	var st TransferStats
	f, err := os.Open(local)
	if err != nil {
		return st, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return st, err
	}
	name := fi.Name()
	start := time.Now()
	res, err := c.Call(ctx, "files.upload.begin", map[string]any{"dir": remoteDir, "name": name, "size": fi.Size(), "overwrite": overwrite})
	if err != nil {
		return st, err
	}
	var b struct {
		Xfer    uint32 `json:"xfer"`
		Channel string `json:"channel"`
		Chunk   int    `json:"chunk"`
	}
	if err := json.Unmarshal(res, &b); err != nil {
		return st, err
	}
	dc, low, err := c.waitFileChannel(ctx, b.Channel)
	if err != nil {
		return st, err
	}
	if b.Chunk <= 0 {
		b.Chunk = 64 << 10
	}
	h := sha256.New()
	buf := make([]byte, b.Chunk)
	frame := make([]byte, fileworker.FrameHeader+b.Chunk)
	var off int64
	for {
		n, rerr := io.ReadFull(f, buf)
		if n > 0 {
			for dc.BufferedAmount() > 4<<20 {
				select {
				case <-low:
				case <-ctx.Done():
					return st, ctx.Err()
				case <-time.After(time.Second):
				}
			}
			h.Write(buf[:n])
			if err := dc.Send(fileworker.EncodeFrame(frame, b.Xfer, uint64(off), buf[:n])); err != nil {
				return st, err
			}
			off += int64(n)
		}
		if rerr == io.EOF || errors.Is(rerr, io.ErrUnexpectedEOF) {
			break
		}
		if rerr != nil {
			return st, rerr
		}
	}
	sum := hex.EncodeToString(h.Sum(nil))
	entry, err := c.Call(ctx, "files.upload.end", map[string]any{"xfer": b.Xfer, "sha256": sum})
	if err != nil {
		return st, err
	}
	return TransferStats{Bytes: off, Elapsed: time.Since(start), SHA256: sum, Entry: entry}, nil
}

// Download baja el archivo remoto (ruta virtual) a local.
func (c *Client) Download(ctx context.Context, remote, local string) (TransferStats, error) {
	return c.download(ctx, "files.download.begin", map[string]any{"path": remote}, local)
}

// GuestDownload baja por un enlace público (sesión invitada, protocol/README.md §15):
// path es relativo al elemento compartido ("" = el archivo compartido).
func (c *Client) GuestDownload(ctx context.Context, path, local string) (TransferStats, error) {
	return c.download(ctx, "guest.download.begin", map[string]any{"path": path}, local)
}

// DownloadRange baja `length` bytes desde `offset` (protocol/README.md §13). Los bytes quedan en
// local en su posición absoluta; snapshot ("" = actual) lee de una instantánea.
func (c *Client) DownloadRange(ctx context.Context, remote, snapshot string, offset, length int64, local string) (TransferStats, error) {
	p := map[string]any{"path": remote, "offset": offset, "length": length}
	if snapshot != "" {
		p["snapshot"] = snapshot
	}
	return c.download(ctx, "files.download.begin", p, local)
}

func (c *Client) download(ctx context.Context, method string, params map[string]any, local string) (TransferStats, error) {
	var st TransferStats
	out, err := os.OpenFile(local, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return st, err
	}
	defer out.Close()
	start := time.Now()
	res, err := c.Call(ctx, method, params)
	if err != nil {
		return st, err
	}
	var b struct {
		Xfer    uint32 `json:"xfer"`
		Channel string `json:"channel"`
		Size    int64  `json:"size"`
		Offset  int64  `json:"offset"`
		Length  *int64 `json:"length"`
	}
	if err := json.Unmarshal(res, &b); err != nil {
		return st, err
	}
	want := b.Size
	if b.Length != nil {
		want = *b.Length
	}
	d := &download{f: out, size: want, done: make(chan struct{}), h: sha256.New(), nextO: b.Offset}
	if want == 0 {
		d.finish(nil)
	}
	cf := c.fs()
	cf.mu.Lock()
	cf.recv[b.Xfer] = d
	early := cf.early[b.Xfer]
	delete(cf.early, b.Xfer)
	cf.mu.Unlock()
	defer func() {
		cf.mu.Lock()
		delete(cf.recv, b.Xfer)
		cf.mu.Unlock()
	}()
	for _, fr := range early {
		_, off, data, _ := fileworker.DecodeFrame(fr)
		d.write(int64(off), data)
	}
	// Errores del servidor llegan como evento files.xfer {state:"error"}.
	go func() {
		for {
			select {
			case ev, ok := <-c.Events:
				if !ok {
					return
				}
				var e struct {
					Topic string `json:"topic"`
					Data  struct {
						Xfer  uint32 `json:"xfer"`
						State string `json:"state"`
						Error string `json:"error"`
					} `json:"data"`
				}
				if json.Unmarshal(ev, &e) == nil && e.Topic == "files.xfer" && e.Data.Xfer == b.Xfer && e.Data.State == "error" {
					d.finish(errors.New(e.Data.Error))
					return
				}
			case <-d.done:
				return
			}
		}
	}()
	select {
	case <-d.done:
	case <-ctx.Done():
		_, _ = c.Call(context.Background(), "files.xfer.cancel", map[string]any{"xfer": b.Xfer})
		return st, ctx.Err()
	}
	if d.err != nil {
		return st, d.err
	}
	return TransferStats{Bytes: d.got, Elapsed: time.Since(start), SHA256: hex.EncodeToString(d.h.Sum(nil))}, nil
}
