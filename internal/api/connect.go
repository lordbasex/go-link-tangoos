package api

import (
	"context"
	"path"
	"strings"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/connect"
	"github.com/lordbasex/go-link-tangoos/internal/fileworker"
	"github.com/lordbasex/go-link-tangoos/pkg/rtcrpc"
)

// TangoConnect (protocol/README.md §15): métodos del NAS para el portal
// (tangoos.go-link.org), los navegadores vinculados y los invitados de enlaces públicos.

// ConnectDeps es lo que necesita RegisterConnect.
type ConnectDeps struct {
	Service *connect.Service
	Bundle  *connect.Bundle
	Server  string // "tangod/<versión>"
	NASID   string
}

// RegisterConnect registra connect.*, bundle.*, shares.links.* y guest.*, e instala la
// compuerta de sesiones remotas en el router.
func (a *API) RegisterConnect(r *rtcrpc.Router, d ConnectDeps) {
	sv := d.Service
	r.Gate = sv.Gate

	type helloR struct {
		Server   string   `json:"server"`
		NASID    string   `json:"nas_id"`
		Name     string   `json:"name"`
		DeviceID string   `json:"deviceId"`
		Methods  []string `json:"methods"`
		Topics   []string `json:"topics"`
		Remote   bool     `json:"remote"`
		State    string   `json:"state,omitempty"`
	}
	// connect.hello reemplaza al `hello` del WebSocket local para quien llega por signalhub.
	r.Handle("connect.hello", rtcrpc.Typed(func(_ context.Context, s *rtcrpc.Session, _ struct{}) (helloR, error) {
		st, _ := s.Get("connect.state").(string)
		return helloR{
			Server: d.Server, NASID: d.NASID, Name: a.d.SysInfo.Read().Hostname, DeviceID: sv.Status().DeviceID,
			Methods: r.Methods(), Topics: a.d.Hub.Topics(), Remote: st != "", State: st,
		}, nil
	}), rtcrpc.Public())

	// ---- administración --------------------------------------------------------------
	r.Handle("connect.status", rtcrpc.Typed(func(_ context.Context, s *rtcrpc.Session, _ struct{}) (connect.Status, error) {
		if err := a.requireAdmin(s); err != nil {
			return connect.Status{}, err
		}
		return sv.Status(), nil
	}))
	type enableP struct {
		Enabled   bool   `json:"enabled"`
		SignalURL string `json:"signalURL"`
		PortalURL string `json:"portalURL"`
	}
	r.Handle("connect.enable", rtcrpc.Typed(func(_ context.Context, s *rtcrpc.Session, p enableP) (connect.Status, error) {
		if err := a.requireAdmin(s); err != nil {
			return connect.Status{}, err
		}
		if err := sv.SetConfig(p.Enabled, p.SignalURL, p.PortalURL); err != nil {
			return connect.Status{}, err
		}
		return sv.Status(), nil
	}))
	type pairR struct {
		Code      string `json:"code"`
		ExpiresAt string `json:"expiresAt"`
	}
	r.Handle("connect.pair.start", rtcrpc.Typed(func(ctx context.Context, s *rtcrpc.Session, _ struct{}) (pairR, error) {
		if err := a.requireAdmin(s); err != nil {
			return pairR{}, err
		}
		code, exp, err := sv.PairStart(ctx)
		if err != nil {
			return pairR{}, err
		}
		return pairR{Code: code, ExpiresAt: exp.UTC().Format(time.RFC3339)}, nil
	}))
	r.Handle("connect.links.list", rtcrpc.Typed(func(_ context.Context, s *rtcrpc.Session, _ struct{}) ([]connect.Link, error) {
		if err := a.requireAdmin(s); err != nil {
			return nil, err
		}
		return sv.Links(), nil
	}))
	type idP struct {
		ID string `json:"id"`
	}
	r.Handle("connect.links.revoke", rtcrpc.Typed(func(_ context.Context, s *rtcrpc.Session, p idP) (struct{}, error) {
		if err := a.requireAdmin(s); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, sv.RevokeLink(p.ID)
	}))

	// ---- vínculo (sesiones remotas; la compuerta decide por estado) -------------------
	type linkP struct {
		Label string `json:"label"`
	}
	r.Handle("connect.link", rtcrpc.Typed(func(_ context.Context, s *rtcrpc.Session, p linkP) (connect.LinkResult, error) {
		return sv.Link(s, p.Label)
	}), rtcrpc.Public())
	type challengeP struct {
		LinkID string `json:"linkId"`
		Nonce  string `json:"nonce"`
	}
	type proofR struct {
		Proof string `json:"proof"`
	}
	r.Handle("connect.challenge", rtcrpc.Typed(func(_ context.Context, s *rtcrpc.Session, p challengeP) (proofR, error) {
		pr, err := sv.Challenge(s, p.LinkID, p.Nonce)
		return proofR{Proof: pr}, err
	}), rtcrpc.Public())
	type resumeP struct {
		LinkID string `json:"linkId"`
		Token  string `json:"token"`
	}
	r.Handle("connect.resume", rtcrpc.Typed(func(_ context.Context, s *rtcrpc.Session, p resumeP) (struct{}, error) {
		return struct{}{}, sv.Resume(s, p.LinkID, p.Token)
	}), rtcrpc.Public())

	// ---- escritorio servido por el NAS --------------------------------------------------
	r.Handle("bundle.manifest", rtcrpc.Typed(func(_ context.Context, _ *rtcrpc.Session, _ struct{}) (connect.ManifestResult, error) {
		if d.Bundle == nil {
			return connect.ManifestResult{}, rtcrpc.Errorf(rtcrpc.CodeNotImplemented, "este NAS no tiene el escritorio embebido")
		}
		return d.Bundle.Manifest(), nil
	}), rtcrpc.Public())
	type getP struct {
		Path string `json:"path"`
	}
	r.Handle("bundle.get", rtcrpc.Typed(func(_ context.Context, s *rtcrpc.Session, p getP) (connect.GetResult, error) {
		ch, ok := s.Get("bundle").(connect.BinSender)
		if !ok || d.Bundle == nil {
			return connect.GetResult{}, rtcrpc.Errorf(rtcrpc.CodeNotImplemented, "esta conexión no tiene canal bundle")
		}
		res, err := d.Bundle.Get(ch, p.Path)
		if err != nil {
			return connect.GetResult{}, rtcrpc.Errorf(fileworker.CodeNotFound, "%v", err)
		}
		return res, nil
	}), rtcrpc.Public())

	// ---- enlaces públicos (dueño) --------------------------------------------------------
	type createP struct {
		Path         string `json:"path"`
		ExpiresAt    string `json:"expiresAt"`
		Password     string `json:"password"`
		MaxDownloads int    `json:"maxDownloads"`
		AllowUpload  bool   `json:"allowUpload"`
	}
	r.Handle("shares.links.create", rtcrpc.Typed(func(ctx context.Context, s *rtcrpc.Session, p createP) (connect.ShareInfo, error) {
		if p.AllowUpload {
			return connect.ShareInfo{}, rtcrpc.Errorf(rtcrpc.CodeNotImplemented, "los enlaces para subir archivos todavía no están disponibles")
		}
		if a.files == nil {
			return connect.ShareInfo{}, rtcrpc.Errorf(rtcrpc.CodeNotImplemented, "Archivos no está disponible")
		}
		// El dueño tiene que poder leer lo que comparte: lo decide su propio worker.
		loc, err := a.files.locate(ctx, s, p.Path, "")
		if err != nil {
			return connect.ShareInfo{}, err
		}
		var e fileworker.Entry
		if err := a.files.call(ctx, s, fileworker.Request{Op: fileworker.OpStat, Root: loc.root, Rel: loc.rel}, &e); err != nil {
			return connect.ShareInfo{}, err
		}
		fe := loc.entry(e)
		if loc.rel == "." {
			fe.Name, fe.Kind = loc.share.Name, "dir"
		}
		return sv.CreateShare(connect.ShareParams{
			Owner: s.User(), Path: fe.Path, Name: fe.Name, Kind: fe.Kind, Size: fe.Size,
			ExpiresAt: p.ExpiresAt, Password: p.Password, MaxDownloads: p.MaxDownloads,
		})
	}))
	type listP struct {
		All bool `json:"all"`
	}
	r.Handle("shares.links.list", rtcrpc.Typed(func(_ context.Context, s *rtcrpc.Session, p listP) ([]connect.ShareInfo, error) {
		all := false
		if p.All {
			if err := a.requireAdmin(s); err != nil {
				return nil, err
			}
			all = true
		}
		return sv.Shares(s.User(), all), nil
	}))
	r.Handle("shares.links.revoke", rtcrpc.Typed(func(_ context.Context, s *rtcrpc.Session, p idP) (struct{}, error) {
		return struct{}{}, sv.RevokeShare(p.ID, s.User(), a.requireAdmin(s) == nil)
	}))

	// ---- invitados de enlaces públicos ----------------------------------------------------
	type openP struct {
		Link   string `json:"link"`   // id del enlace (la parte después del punto en /s/<device>.<id>)
		Secret string `json:"secret"` // el #fragmento
	}
	type guestInfo struct {
		Name          string `json:"name"`
		Kind          string `json:"kind"`
		Size          int64  `json:"size"`
		NAS           string `json:"nas"`
		ExpiresAt     string `json:"expiresAt,omitempty"`
		NeedsPassword bool   `json:"needsPassword"`
		Unlocked      bool   `json:"unlocked"`
		DownloadsLeft *int   `json:"downloadsLeft,omitempty"`
	}
	info := func(s *rtcrpc.Session, l connect.ShareLink) guestInfo {
		gi := guestInfo{Name: l.Name, Kind: l.Kind, Size: l.Size, NAS: a.d.SysInfo.Read().Hostname, ExpiresAt: l.ExpiresAt, NeedsPassword: l.PasswordHash != ""}
		gi.Unlocked, _ = s.Get("guest.unlocked").(bool)
		if l.MaxDownloads > 0 {
			left := max(l.MaxDownloads-l.Downloads, 0)
			gi.DownloadsLeft = &left
		}
		return gi
	}
	r.Handle("guest.open", rtcrpc.Typed(func(_ context.Context, s *rtcrpc.Session, p openP) (guestInfo, error) {
		l, err := sv.GuestOpen(s, p.Link, p.Secret)
		if err != nil {
			return guestInfo{}, err
		}
		s.Set("files.as", l.Owner) // los archivos se leen con los permisos del dueño
		return info(s, l), nil
	}), rtcrpc.Public())
	r.Handle("guest.info", rtcrpc.Typed(func(_ context.Context, s *rtcrpc.Session, _ struct{}) (guestInfo, error) {
		l, err := sv.GuestPeek(s)
		if err != nil {
			return guestInfo{}, err
		}
		return info(s, l), nil
	}), rtcrpc.Public())
	type unlockP struct {
		Password string `json:"password"`
	}
	r.Handle("guest.unlock", rtcrpc.Typed(func(_ context.Context, s *rtcrpc.Session, p unlockP) (struct{}, error) {
		return struct{}{}, sv.GuestUnlock(s, p.Password)
	}), rtcrpc.Public())
	type gPathP struct {
		Path string `json:"path"` // relativa al elemento compartido ("" = el elemento)
	}
	r.Handle("guest.list", rtcrpc.Typed(func(ctx context.Context, s *rtcrpc.Session, p gPathP) ([]FileEntry, error) {
		l, err := sv.GuestLink(s)
		if err != nil {
			return nil, err
		}
		if l.Kind != "dir" {
			return nil, rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "este enlace es de un archivo")
		}
		vp, err := guestScope(l.Path, p.Path)
		if err != nil {
			return nil, err
		}
		entries, err := a.guestList(ctx, s, vp)
		if err != nil {
			return nil, err
		}
		for i := range entries { // rutas relativas al elemento compartido: el invitado no ve el resto
			entries[i].Path = strings.TrimPrefix(strings.TrimPrefix(entries[i].Path, l.Path), "/")
		}
		return entries, nil
	}), rtcrpc.Public())
	type gDownP struct {
		Path   string `json:"path"`
		Offset *int64 `json:"offset"`
		Length *int64 `json:"length"`
	}
	type downR struct {
		Xfer    uint32 `json:"xfer"`
		Channel string `json:"channel"`
		Size    int64  `json:"size"`
		Chunk   int    `json:"chunk"`
		Offset  int64  `json:"offset"`
		Length  int64  `json:"length"`
	}
	r.Handle("guest.download.begin", rtcrpc.Typed(func(ctx context.Context, s *rtcrpc.Session, p gDownP) (downR, error) {
		l, err := sv.GuestLink(s)
		if err != nil {
			return downR{}, err
		}
		vp, err := guestScope(l.Path, p.Path)
		if err != nil {
			return downR{}, err
		}
		if l.Kind != "dir" && vp != l.Path {
			return downR{}, rtcrpc.Errorf(fileworker.CodeNotFound, "este enlace es de un archivo")
		}
		if a.files == nil {
			return downR{}, rtcrpc.Errorf(rtcrpc.CodeNotImplemented, "Archivos no está disponible")
		}
		// Cuenta como descarga el pedido desde el principio (los rangos siguientes, no).
		if p.Offset == nil || *p.Offset == 0 {
			if err := sv.CountDownload(l.ID); err != nil {
				return downR{}, err
			}
		}
		x, err := a.files.downloadBegin(ctx, s, vp, "", p.Offset, p.Length)
		if err != nil {
			return downR{}, err
		}
		if a.m4 != nil {
			a.m4.audit("enlace:"+l.ID+" ("+l.Owner+")", s.RemoteIP, "guest", "share.link.download", vp, "ok")
		}
		return downR{Xfer: x.id, Channel: chanLabel(x.ch), Size: x.size, Chunk: chunkSize, Offset: x.from, Length: x.to - x.from}, nil
	}), rtcrpc.Public())
}

// guestScope traduce una ruta relativa del invitado a una ruta virtual dentro del
// elemento compartido; cualquier intento de salir de él es NOT_FOUND.
func guestScope(base, rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if strings.ContainsRune(rel, 0) {
		return "", rtcrpc.Errorf(rtcrpc.CodeInvalidParams, "ruta inválida")
	}
	if rel == "" || rel == "." || rel == "/" {
		return base, nil
	}
	vp := path.Clean(base + "/" + strings.TrimPrefix(rel, "/"))
	if vp != base && !strings.HasPrefix(vp, strings.TrimRight(base, "/")+"/") {
		return "", rtcrpc.Errorf(fileworker.CodeNotFound, "no existe")
	}
	return vp, nil
}

// guestList lista una carpeta con el worker del dueño del enlace (sin vigilarla).
func (a *API) guestList(ctx context.Context, s *rtcrpc.Session, vp string) ([]FileEntry, error) {
	if a.files == nil {
		return nil, rtcrpc.Errorf(rtcrpc.CodeNotImplemented, "Archivos no está disponible")
	}
	fa := a.files
	loc, err := fa.locate(ctx, s, vp, "")
	if err != nil {
		return nil, err
	}
	out := []FileEntry{}
	for off := 0; off < 20_000; {
		var page fileworker.ListResult
		if err := fa.call(ctx, s, fileworker.Request{Op: fileworker.OpList, Root: loc.root, Rel: loc.rel, Offset: off, Limit: 300}, &page); err != nil {
			return nil, err
		}
		for _, e := range page.Entries {
			out = append(out, loc.entry(e))
		}
		off += len(page.Entries)
		if !page.More || len(page.Entries) == 0 {
			break
		}
	}
	return out, nil
}
