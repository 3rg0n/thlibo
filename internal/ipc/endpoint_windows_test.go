//go:build windows

package ipc

import (
	"strings"
	"testing"
)

// A3 (Windows): the pipe ACL must grant the current user (not Everyone)
// and must not include a Deny-Everyone ACE (which would paradoxically
// deny the owner). This test checks the generated SDDL string, which is
// what the pipe's security descriptor is built from.
func TestSDDLGrantsUserOnly(t *testing.T) {
	sddl, err := sddlForKind(EndpointInference)
	if err != nil {
		t.Fatalf("sddlForKind: %v", err)
	}
	if !strings.HasPrefix(sddl, "D:PAI(") {
		t.Errorf("SDDL missing PAI protected flag: %q", sddl)
	}
	if !strings.Contains(sddl, "(A;;GRGW;;;S-1-") {
		t.Errorf("SDDL missing allow-ACE for user SID: %q", sddl)
	}
	if strings.Contains(sddl, "(D;") {
		t.Errorf("SDDL should not contain Deny ACEs (would deny owner via Everyone): %q", sddl)
	}
	if strings.Contains(sddl, ";;;WD)") {
		t.Errorf("SDDL should not reference WD (Everyone): %q", sddl)
	}
}
