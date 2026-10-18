package security

import "testing"

// A public share link must never escalate to the blanket embedded grant.
func TestGuestPermissionSetPublicIsReadOnly(t *testing.T) {
	set := GuestPermissionSet(ScopePublic)
	if set.Admin() {
		t.Fatal("public guest token must not be admin")
	}
	for perm := range set {
		if perm == "* on *" {
			t.Fatalf("public guest token must not hold %q", perm)
		}
		if !hasOnlyReadActions(perm) {
			t.Fatalf("public guest scope leaked a non-read permission: %q", perm)
		}
	}
	if !set.Has(ActionRead + " on " + ResDashboard) {
		t.Fatal("public guest must be able to read the shared dashboard")
	}
	if !set.Has(ActionQueryFormData + " on " + ResChart) {
		t.Fatal("public guest must be able to run the chart queries behind the tiles")
	}
}

// Everything a public viewer must *not* reach.
func TestGuestPermissionSetPublicBlocksSensitiveResources(t *testing.T) {
	set := GuestPermissionSet(ScopePublic)
	for _, res := range []string{ResUser, ResRole, ResLog, ResSqllab, ResReportSchedule, ResAlert} {
		if set.Has(ActionRead + " on " + res) {
			t.Fatalf("public guest scope must not grant read on %s", res)
		}
	}
}

// The embedded flow keeps its historical blanket grant.
func TestGuestPermissionSetEmbeddedUnchanged(t *testing.T) {
	set := GuestPermissionSet(ScopeEmbedded)
	if !set.Admin() {
		t.Fatal("embedded guest token must keep the admin grant")
	}
}

func hasOnlyReadActions(perm string) bool {
	if perm == PermAllDatasourceAccess {
		return true
	}
	return len(perm) > len(ActionRead) && perm[:len(ActionRead)] == ActionRead ||
		perm == ActionQueryFormData+" on "+ResChart
}
