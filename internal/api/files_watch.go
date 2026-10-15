package api

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/fileworker"
)

// Cambios externos (protocol/README.md §13): lo que entra por SMB, NFS o SFTP no pasa
// por tangod, así que files.changed no salía. Cada carpeta que un usuario listó en los
// últimos 60 s se vigila con inotify en su file-worker (long-poll de hasta 25 s); al
// cambiar se avisa como si el cambio lo hubiera hecho TangoOS.

const (
	watchRecent  = 60 * time.Second
	watchPoll    = 25 // segundos por long-poll
	watchBackoff = 2 * time.Second
)

type watchDir struct {
	loc  location
	last time.Time
}

// noteDir registra que user abrió una carpeta y arranca su vigilante si no corre.
func (fa *filesAPI) noteDir(user string, loc location) {
	if user == "" {
		return
	}
	key := loc.root + "\x00" + loc.rel
	fa.wmu.Lock()
	defer fa.wmu.Unlock()
	if fa.recent == nil {
		fa.recent, fa.watching = map[string]map[string]*watchDir{}, map[string]bool{}
	}
	m := fa.recent[user]
	if m == nil {
		m = map[string]*watchDir{}
		fa.recent[user] = m
	}
	m[key] = &watchDir{loc: loc, last: time.Now()}
	if !fa.watching[user] {
		fa.watching[user] = true
		go fa.watchLoop(user)
	}
}

// dirsFor devuelve las carpetas recientes de user (las más nuevas primero, hasta el
// máximo del worker) y olvida las viejas.
func (fa *filesAPI) dirsFor(user string) []location {
	fa.wmu.Lock()
	defer fa.wmu.Unlock()
	m := fa.recent[user]
	list := make([]*watchDir, 0, len(m))
	for k, d := range m {
		if time.Since(d.last) > watchRecent {
			delete(m, k)
			continue
		}
		list = append(list, d)
	}
	if len(list) == 0 {
		delete(fa.recent, user)
		fa.watching[user] = false
		return nil
	}
	sort.Slice(list, func(i, j int) bool { return list[i].last.After(list[j].last) })
	if len(list) > fileworker.MaxWatchDirs {
		list = list[:fileworker.MaxWatchDirs]
	}
	out := make([]location, len(list))
	for i, d := range list {
		out[i] = d.loc
	}
	return out
}

func (fa *filesAPI) watchLoop(user string) {
	for {
		dirs := fa.dirsFor(user)
		if dirs == nil {
			return // nadie mira carpetas: el vigilante se va (noteDir lo relanza)
		}
		req := fileworker.Request{Op: fileworker.OpWatch, Limit: watchPoll}
		for _, l := range dirs {
			req.Roots = append(req.Roots, l.root)
			req.Rels = append(req.Rels, l.rel)
		}
		ctx, cancel := context.WithTimeout(context.Background(), (watchPoll+5)*time.Second)
		w, err := fa.worker(ctx, user)
		var res fileworker.WatchResult
		if err == nil {
			_, err = w.Call(ctx, req, &res)
		}
		cancel()
		if err != nil {
			time.Sleep(watchBackoff)
			continue
		}
		for _, i := range res.Changed {
			if i >= 0 && i < len(dirs) {
				fa.changed(dirs[i].share, dirs[i].rel)
			}
		}
	}
}

// ForgetUnder deja de vigilar las carpetas que están bajo prefix (un volumen o una carpeta
// compartida que se va a borrar): si no, el vigilante relanza un worker que retiene fds
// adentro y el volumen no se puede desmontar.
func (fa *filesAPI) ForgetUnder(prefix string) {
	fa.wmu.Lock()
	defer fa.wmu.Unlock()
	for _, m := range fa.recent {
		for k, d := range m {
			if d.loc.root == strings.TrimSuffix(prefix, "/") || strings.HasPrefix(d.loc.root, prefix) {
				delete(m, k)
			}
		}
	}
}
