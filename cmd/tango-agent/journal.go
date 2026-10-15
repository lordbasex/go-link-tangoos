package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// journal registra el inicio y el fin de cada operación con efectos (docs/02).
// Si el agente muere a mitad de una, al arrancar queda marcada como "interrupted":
// nunca se reintenta sola, la UI muestra qué quedó hecho.
//
// Esta fase solo tiene ops de lectura y no lo usa; queda listo para las destructivas.
type journal struct{ dir string }

type journalEntry struct {
	OpID    string          `json:"op_id"`
	Op      string          `json:"op"`
	Args    json.RawMessage `json:"args,omitempty"`
	State   string          `json:"state"` // running | succeeded | failed | interrupted
	Started time.Time       `json:"started"`
	Ended   *time.Time      `json:"ended,omitempty"`
	Error   string          `json:"error,omitempty"`
}

var opIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func openJournal(dir string) (*journal, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &journal{dir: dir}, nil
}

func (j *journal) path(opID string) string { return filepath.Join(j.dir, opID+".json") }

// write escribe de forma atómica y durable: archivo temporal, fsync, rename, fsync del dir.
func (j *journal) write(e *journalEntry) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(j.dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), j.path(e.OpID)); err != nil {
		return err
	}
	d, err := os.Open(j.dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// begin registra una operación en curso. Un op_id repetido es un reintento: se rechaza.
func (j *journal) begin(opID, op string, args json.RawMessage) error {
	if !opIDRe.MatchString(opID) {
		return fmt.Errorf("op_id inválido")
	}
	if _, err := os.Stat(j.path(opID)); err == nil {
		return fmt.Errorf("la operación %s ya existe en el journal", opID)
	}
	return j.write(&journalEntry{OpID: opID, Op: op, Args: args, State: "running", Started: time.Now().UTC()})
}

// end cierra una operación.
func (j *journal) end(opID string, opErr error) error {
	e, err := j.read(opID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	e.Ended = &now
	e.State = "succeeded"
	if opErr != nil {
		e.State, e.Error = "failed", opErr.Error()
	}
	return j.write(e)
}

func (j *journal) read(opID string) (*journalEntry, error) {
	b, err := os.ReadFile(j.path(opID))
	if err != nil {
		return nil, err
	}
	var e journalEntry
	if err := json.Unmarshal(b, &e); err != nil {
		return nil, err
	}
	return &e, nil
}

// recover marca como interrumpidas las operaciones que quedaron "running".
func (j *journal) recover() (int, error) {
	ents, err := os.ReadDir(j.dir)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, de := range ents {
		name := de.Name()
		if strings.HasPrefix(name, ".tmp-") {
			_ = os.Remove(filepath.Join(j.dir, name))
			continue
		}
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		e, err := j.read(strings.TrimSuffix(name, ".json"))
		if err != nil || e.State != "running" {
			continue
		}
		now := time.Now().UTC()
		e.State, e.Ended, e.Error = "interrupted", &now, "el agente se detuvo durante la operación"
		if err := j.write(e); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
