package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/lordbasex/go-link-tangoos/internal/storage"
)

// migrateVolumeIDs pasa las referencias guardadas a volúmenes al ID estable de §11
// (UUID del Btrfs; antes era el nombre del kernel "md127" o "btrfs-<uuid>").
// Hoy solo apps.json guarda volúmenes; storage.json usa puntos de montaje.
// Es idempotente y deja una copia apps.json.bak-m22 la primera vez que cambia algo.
func migrateVolumeIDs(d Deps) {
	path := filepath.Join(d.StateDir, "apps.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var list []map[string]any
	if json.Unmarshal(b, &list) != nil || len(list) == 0 {
		return
	}
	// Al arrancar, los volúmenes pueden montarse unos segundos después que tangod:
	// esperar hasta 20 s a que aparezcan los puntos de montaje que usan las apps.
	var vols []storage.Volume
	for range 20 {
		vols, _ = d.Inventory.Volumes(context.Background())
		if allMounted(list, vols) {
			break
		}
		time.Sleep(time.Second)
	}
	out, changed := remapVolumes(list, vols)
	if !changed {
		return
	}
	nb, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return
	}
	bak := path + ".bak-m22"
	if _, err := os.Stat(bak); os.IsNotExist(err) {
		_ = os.WriteFile(bak, b, 0o600)
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, nb, 0o600) == nil {
		_ = os.Rename(tmp, path)
		if d.Log != nil {
			d.Log.Info("apps.json: volúmenes migrados a IDs estables")
		}
	}
}

func allMounted(list []map[string]any, vols []storage.Volume) bool {
	mounts := map[string]bool{}
	for _, v := range vols {
		mounts[v.Mount] = true
	}
	for _, it := range list {
		if m, _ := it["mount"].(string); m != "" && !mounts[m] {
			return false
		}
	}
	return true
}

// remapVolumes reemplaza el campo "volume" de cada entrada por el ID actual del
// volumen: primero por punto de montaje (estable), después por las formas viejas del ID.
func remapVolumes(list []map[string]any, vols []storage.Volume) ([]map[string]any, bool) {
	current := map[string]bool{}
	byMount := map[string]string{}
	byLegacy := map[string]string{}
	for _, v := range vols {
		current[v.ID] = true
		if v.Mount != "" {
			byMount[v.Mount] = v.ID
		}
		byLegacy["btrfs-"+v.ID] = v.ID
		byLegacy[v.Name] = v.ID
	}
	changed := false
	for _, it := range list {
		vol, _ := it["volume"].(string)
		if vol == "" || current[vol] {
			continue
		}
		mount, _ := it["mount"].(string)
		if id := byMount[mount]; id != "" {
			it["volume"], changed = id, true
		} else if id := byLegacy[vol]; id != "" {
			it["volume"], changed = id, true
		}
	}
	return list, changed
}
