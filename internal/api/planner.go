package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"
)

// PlanTTL es cuánto vale un plan de volumen (protocol/README.md §7).
const PlanTTL = 5 * time.Minute

// planDisk es un disco tal como lo vio el plan.
type planDisk struct {
	ID          string // Disk.id (el que conoce la UI)
	ByID        string // nombre en /dev/disk/by-id (el que conoce el agente)
	Model       string
	Serial      string
	Size        uint64
	Content     string
	Fingerprint string
}

// Tipos de plan: crear un volumen o eliminarlo. Un token de un tipo nunca sirve
// para el otro (el tipo entra en el HMAC).
const (
	planCreate = "create"
	planDelete = "delete"
)

type volumePlan struct {
	ID      string
	Kind    string
	Name    string
	Profile string
	Disks   []planDisk
	Expires time.Time
	Token   string
	// Solo en planes de borrado.
	VolumeID string
	Mount    string
	Shares   []string
	// Aux son datos extra firmados de los planes de mantenimiento (§11): el disco a
	// reemplazar o la cantidad de discos final de una ampliación.
	Aux []string
}

// planner guarda los planes en memoria y firma sus tokens con una clave que solo
// existe en este proceso: un plan no sobrevive a un reinicio de tangod (a propósito).
type planner struct {
	key []byte
	now func() time.Time

	mu    sync.Mutex
	plans map[string]*volumePlan
}

func newPlanner() *planner {
	k := make([]byte, 32)
	_, _ = rand.Read(k)
	return &planner{key: k, now: time.Now, plans: map[string]*volumePlan{}}
}

// token = HMAC(clave, plan_id + nombre + perfil + by-id:tamaño:huella de cada disco).
func (p *planner) token(v *volumePlan) string {
	m := hmac.New(sha256.New, p.key)
	fmt.Fprintf(m, "%s|%s|%s|%s|%s|%s", v.ID, v.Kind, v.Name, v.Profile, v.VolumeID, v.Mount)
	for _, d := range v.Disks {
		fmt.Fprintf(m, "|%s:%d:%s", d.ByID, d.Size, d.Fingerprint)
	}
	for _, x := range v.Aux {
		fmt.Fprintf(m, "|aux:%s", x)
	}
	return hex.EncodeToString(m.Sum(nil))
}

func (p *planner) add(name, profile string, disks []planDisk) *volumePlan {
	return p.store(&volumePlan{Kind: planCreate, Name: name, Profile: profile, Disks: disks})
}

// addDelete registra un plan para eliminar un volumen.
func (p *planner) addDelete(name, volumeID, mount string, disks []planDisk, shares []string) *volumePlan {
	return p.store(&volumePlan{Kind: planDelete, Name: name, Disks: disks, VolumeID: volumeID, Mount: mount, Shares: shares})
}

func (p *planner) store(v *volumePlan) *volumePlan {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	v.ID, v.Expires = "p_"+hex.EncodeToString(b), p.now().Add(PlanTTL)
	v.Token = p.token(v)
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, old := range p.plans { // poda de vencidos
		if p.now().After(old.Expires) {
			delete(p.plans, id)
		}
	}
	p.plans[v.ID] = v
	return v
}

// Errores de confirmación (códigos estables del contrato).
const (
	codePlanExpired  = "PLAN_EXPIRED"
	codeMismatch     = "CONFIRMATION_MISMATCH"
	codeChanged      = "DEVICE_CHANGED"
	codeNotEligible  = "DEVICE_NOT_ELIGIBLE"
	codeVolumeExists = "VOLUME_EXISTS"
)

// check valida token y nombre escrito sin consumir el plan.
func (p *planner) check(id, token, typed string) (*volumePlan, string, string) {
	return p.checkKind(planCreate, id, token, typed)
}

// checkKind es check para un tipo de plan: un plan de borrado no crea y viceversa.
func (p *planner) checkKind(kind, id, token, typed string) (*volumePlan, string, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	v, ok := p.plans[id]
	if !ok || p.now().After(v.Expires) || v.Kind != kind {
		delete(p.plans, id)
		return nil, codePlanExpired, "el plan venció o no existe: volvé a revisar los discos"
	}
	// Se recalcula sobre los datos guardados: un plan manipulado no coincide.
	if !hmac.Equal([]byte(token), []byte(p.token(v))) {
		return nil, codeMismatch, "la confirmación no corresponde a este plan"
	}
	if strings.TrimSpace(typed) != v.Name {
		return nil, codeMismatch, "el nombre escrito no coincide con el del volumen"
	}
	return v, "", ""
}

// consume quita el plan: un token sirve una sola vez.
func (p *planner) consume(id string) {
	p.mu.Lock()
	delete(p.plans, id)
	p.mu.Unlock()
}
