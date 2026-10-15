package main

import (
	"testing"

	"github.com/lordbasex/go-link-tangoos/pkg/agentproto"
)

func TestValidateUserDelete(t *testing.T) {
	good := []agentproto.UserDeleteArgs{
		{Name: "sofia"},
		{Name: "sofia", ReassignTo: "admin", Mounts: []string{"/volume1", "/volume2"}},
	}
	for _, a := range good {
		if e := validateUserDelete(&a); e != nil {
			t.Fatalf("%+v: %v", a, e)
		}
	}
	bad := []agentproto.UserDeleteArgs{
		{Name: "root"},
		{Name: "../x"},
		{Name: "sofia", ReassignTo: "sofia"},
		{Name: "sofia", ReassignTo: "admin", Mounts: []string{"/etc"}},
		{Name: "sofia", ReassignTo: "admin", Mounts: []string{"/volume1/@shares"}},
	}
	for _, a := range bad {
		if e := validateUserDelete(&a); e == nil {
			t.Fatalf("%+v must be rejected", a)
		}
	}
}
