package audit

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestTargetNeverLeaksSecrets(t *testing.T) {
	p := json.RawMessage(`{"name":"sofia","password":"S3cret!pass","token":"abc","confirm_token":"xyz","chap":{"secret":"zzz"},"share":"fotos"}`)
	got := Target(p)
	for _, bad := range []string{"S3cret", "abc", "xyz", "zzz"} {
		if strings.Contains(got, bad) {
			t.Fatalf("target %q leaks %q", got, bad)
		}
	}
	if !strings.Contains(got, "share=fotos") || !strings.Contains(got, "name=sofia") {
		t.Fatalf("target %q misses the useful keys", got)
	}
}

func TestShouldRecord(t *testing.T) {
	cases := map[string]bool{"auth.login": true, "auth.resume": false, "files.list": false, "storage.volumes.create": true, "notify.channels.set": true}
	for m, want := range cases {
		if got := ShouldRecord(m, false); got != want {
			t.Errorf("%s: got %v want %v", m, got, want)
		}
	}
	if !ShouldRecord("files.list", true) || !ShouldRecord("auth.resume", true) {
		t.Fatal("denied calls must always be recorded")
	}
}

func TestRecordListAndRotate(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var pushed int
	l.OnEvent = func(Event) { pushed++ }
	for i := 0; i < 5; i++ {
		l.Record(Event{Actor: "admin", Method: "shares.create", Target: "name=Fotos", Result: "ok"})
	}
	l.Record(Event{Actor: "sofia", Method: "auth.login", Result: "error"})
	evs, err := l.List(Query{Limit: 10})
	if err != nil || len(evs) != 6 || pushed != 6 {
		t.Fatalf("list: %v %d %d", err, len(evs), pushed)
	}
	if evs[0].Actor != "sofia" {
		t.Fatalf("newest first: %+v", evs[0])
	}
	if f, _ := l.List(Query{Q: "SOFIA", Limit: 10}); len(f) != 1 {
		t.Fatalf("filter: %d", len(f))
	}
	st, _ := os.Stat(dir + "/audit.jsonl")
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("perm %v", st.Mode().Perm())
	}
	// Rotación forzada.
	l.mu.Lock()
	l.size = MaxBytes
	l.mu.Unlock()
	l.Record(Event{Actor: "admin", Method: "after.rotate", Result: "ok"})
	if _, err := os.Stat(dir + "/audit.jsonl.1"); err != nil {
		t.Fatal("no rotation file")
	}
	all, _ := l.List(Query{Limit: 100})
	if len(all) != 7 || all[0].Method != "after.rotate" {
		t.Fatalf("after rotate: %d %+v", len(all), all[0])
	}
}
