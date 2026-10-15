package api

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"testing"

	"github.com/lordbasex/go-link-tangoos/internal/auth"
)

func TestPlanProfileRules(t *testing.T) {
	ag := &fakeAgent{fp: map[string]string{}}
	s := newTestStorage(t, ag)
	ids := diskIDs(t, s)
	two := []string{ids["sdb"], ids["sdc"]}
	for _, p := range []string{"raid5", "raid6", "raid10"} {
		if _, err := s.plan(context.Background(), "volume2", p, two); code(err) != "INVALID_PARAMS" {
			t.Errorf("%s con 2 discos: %v", p, err)
		}
	}
	plan, err := s.plan(context.Background(), "volume2", "raid1", two)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Summary.Usable != 17179869184 || len(plan.Summary.Notes) != 0 {
		t.Errorf("usable %d notes %v", plan.Summary.Usable, plan.Summary.Notes)
	}
	// Un token de creación no sirve para borrar.
	if _, err := s.deleteVolume(context.Background(), plan.PlanID, plan.ConfirmToken, "volume2"); code(err) != codePlanExpired {
		t.Errorf("plan de creación usado para borrar: %v", err)
	}
}

func TestPermissionsExpand(t *testing.T) {
	ag := &fakeAgent{fp: map[string]string{}}
	s := newTestStorage(t, ag)
	st, err := auth.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []auth.User{{Name: "admin", Full: "Admin", Admin: true}, {Name: "sofia", Full: "Sofía", Groups: []string{"familia"}}, {Name: "martin", Full: "Martín", Groups: []string{"familia"}}} {
		if err := st.Add(u, "una-contraseña-larga"); err != nil {
			t.Fatal(err)
		}
	}
	s.a.d.Auth = st
	s.a.d.Log = slog.New(slog.NewTextHandler(io.Discard, nil))

	rw, ro, err := s.expand(defaultPerms())
	if err != nil || !slices.Equal(rw, []string{"admin"}) || len(ro) != 0 {
		t.Fatalf("por defecto: rw=%v ro=%v err=%v", rw, ro, err)
	}
	rw, ro, err = s.expand([]PermEntry{
		{Kind: "group", Name: "administrators", Access: "rw"},
		{Kind: "group", Name: "familia", Access: "ro"},
		{Kind: "user", Name: "sofia", Access: "rw"},
		{Kind: "user", Name: "martin", Access: "none"},
	})
	if err != nil || !slices.Equal(rw, []string{"admin", "sofia"}) || len(ro) != 0 {
		t.Fatalf("rw=%v ro=%v err=%v", rw, ro, err)
	}
	for _, bad := range [][]PermEntry{{{Kind: "user", Name: "nadie", Access: "rw"}}, {{Kind: "user", Name: "sofia", Access: "admin"}}, {{Kind: "robot", Name: "x", Access: "rw"}}} {
		if _, _, err := s.expand(bad); code(err) != "INVALID_PARAMS" {
			t.Errorf("%v: %v", bad, err)
		}
	}
}
