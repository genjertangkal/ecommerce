package policy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func input(subjectRoles, subjectTeams []string, action, ownerTeam string) Input {
	return Input{
		Subject:  Subject{ID: "u-1", Roles: subjectRoles, Teams: subjectTeams},
		Resource: Resource{Type: "product", ID: "p-1", OwnerTeam: ownerTeam},
		Action:   action,
	}
}

func TestUnknownActionIsDenied(t *testing.T) {
	t.Parallel()

	e := NewPolicyEngine("rego", []string{string(DecisionActionPermission)})
	d, err := e.Evaluate(DecisionActionPermission, input([]string{"catalog_admin"}, []string{"commerce"}, "frobnicate", "commerce"))
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if d.Allow {
		t.Error("an unknown action was allowed")
	}
	if !strings.Contains(d.Reason, "unknown action") {
		t.Errorf("Reason = %q, want it to name the unknown action", d.Reason)
	}
}

func TestNoRolesIsDenied(t *testing.T) {
	t.Parallel()

	e := NewPolicyEngine("rego", nil)
	d, _ := e.Evaluate(DecisionResourceAccess, input(nil, []string{"commerce"}, "read", "commerce"))

	if d.Allow {
		t.Error("a subject with no roles was allowed")
	}
	if d.Reason != "subject has no roles" {
		t.Errorf("Reason = %q, want %q", d.Reason, "subject has no roles")
	}
}

func TestPlatformRoleGrantsAccess(t *testing.T) {
	t.Parallel()

	e := NewPolicyEngine("rego", nil)
	for _, role := range []string{"platform_admin", "security_admin"} {
		d, _ := e.Evaluate(DecisionActionPermission, input([]string{role}, []string{"security"}, "write", "commerce"))
		if !d.Allow {
			t.Errorf("role %q was denied write, want allow", role)
		}
	}
}

func TestTeamWriteWithinOwnTeam(t *testing.T) {
	t.Parallel()

	e := NewPolicyEngine("rego", nil)
	d, _ := e.Evaluate(DecisionActionPermission, input([]string{"catalog_admin"}, []string{"commerce"}, "write", "commerce"))

	if !d.Allow {
		t.Errorf("write within the owning team was denied: %s", d.Reason)
	}
}

func TestTeamWriteOutsideOwnTeamIsDenied(t *testing.T) {
	t.Parallel()

	e := NewPolicyEngine("rego", nil)
	d, _ := e.Evaluate(DecisionActionPermission, input([]string{"catalog_admin"}, []string{"identity"}, "write", "commerce"))

	if d.Allow {
		t.Error("cross-team write was allowed")
	}
	if d.Reason != "default deny" {
		t.Errorf("Reason = %q, want %q", d.Reason, "default deny")
	}
}

func TestViewerCannotWrite(t *testing.T) {
	t.Parallel()

	e := NewPolicyEngine("rego", nil)
	d, _ := e.Evaluate(DecisionActionPermission, input([]string{"catalog_viewer"}, []string{"commerce"}, "write", "commerce"))

	if d.Allow {
		t.Error("a viewer was allowed to write")
	}
}

func TestViewerCanReadOwnTeam(t *testing.T) {
	t.Parallel()

	e := NewPolicyEngine("rego", nil)
	d, _ := e.Evaluate(DecisionResourceAccess, input([]string{"catalog_viewer"}, []string{"commerce"}, "read", "commerce"))

	if !d.Allow {
		t.Errorf("a viewer was denied a read within its own team: %s", d.Reason)
	}
}

func TestCrossTeamReadIsAllowed(t *testing.T) {
	t.Parallel()

	e := NewPolicyEngine("rego", nil)
	d, _ := e.Evaluate(DecisionResourceAccess, input([]string{"catalog_viewer"}, []string{"identity"}, "read", "commerce"))

	if !d.Allow {
		t.Errorf("cross-team read was denied: %s", d.Reason)
	}
	if !strings.Contains(d.Reason, "cross-team") {
		t.Errorf("Reason = %q, want it to explain the cross-team read", d.Reason)
	}
}

func TestDeleteRequiresPlatformRole(t *testing.T) {
	t.Parallel()

	e := NewPolicyEngine("rego", nil)
	d, _ := e.Evaluate(DecisionActionPermission, input([]string{"catalog_admin"}, []string{"commerce"}, "delete", "commerce"))

	if d.Allow {
		t.Error("delete was allowed without a platform role")
	}
	if !strings.Contains(d.Reason, "delete requires a platform role") {
		t.Errorf("Reason = %q, want it to name the required role", d.Reason)
	}
}

func TestAdminRequiresMFA(t *testing.T) {
	t.Parallel()

	e := NewPolicyEngine("rego", nil)

	d, _ := e.Evaluate(DecisionActionPermission, input([]string{"catalog_admin"}, []string{"commerce"}, "admin", "commerce"))
	if d.Allow {
		t.Error("admin was allowed without MFA")
	}
	if d.Reason != "admin requires MFA" {
		t.Errorf("Reason = %q, want %q", d.Reason, "admin requires MFA")
	}

	withMFA := input([]string{"platform_admin"}, []string{"security"}, "admin", "commerce")
	withMFA.Context.MFAVerified = true
	d, _ = e.Evaluate(DecisionActionPermission, withMFA)
	if !d.Allow {
		t.Errorf("admin with MFA and a platform role was denied: %s", d.Reason)
	}
}

func TestLoadPolicy(t *testing.T) {
	t.Parallel()

	e := NewPolicyEngine("rego", nil)
	if err := e.LoadPolicy("access", "package p\n"); err != nil {
		t.Fatalf("LoadPolicy() error = %v", err)
	}
	if got := e.PolicyNames(); len(got) != 1 || got[0] != "access" {
		t.Errorf("PolicyNames() = %v, want [access]", got)
	}
}

func TestLoadPolicyValidation(t *testing.T) {
	t.Parallel()

	e := NewPolicyEngine("rego", nil)

	if err := e.LoadPolicy("", "package p\n"); err == nil {
		t.Error("LoadPolicy with an empty name = nil error, want an error")
	}
	if err := e.LoadPolicy("access", ""); err == nil {
		t.Error("LoadPolicy with empty source = nil error, want an error")
	}
}

func TestUnsupportedPolicyLanguage(t *testing.T) {
	t.Parallel()

	e := NewPolicyEngine("sql", nil)
	if err := e.LoadPolicy("access", "SELECT 1"); err == nil {
		t.Error("LoadPolicy on a non-rego engine = nil error, want an error")
	}
}

func TestLoadPolicyFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "policy.rego")
	if err := os.WriteFile(path, []byte("package ecommerce.security\n"), 0o600); err != nil {
		t.Fatalf("writing policy: %v", err)
	}

	e := NewPolicyEngine("rego", nil)
	if err := e.LoadPolicyFile("access", path); err != nil {
		t.Fatalf("LoadPolicyFile() error = %v", err)
	}
}

func TestLoadPolicyFileMissing(t *testing.T) {
	t.Parallel()

	e := NewPolicyEngine("rego", nil)
	if err := e.LoadPolicyFile("access", filepath.Join(t.TempDir(), "nope.rego")); err == nil {
		t.Error("LoadPolicyFile on a missing file = nil error, want an error")
	}
}

// policy.rego shipped in this package previously contained Go source code, so
// the policy was never actually parseable as Rego. This checks that the shipped
// module is now real Rego.
func TestShippedRegoIsRego(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("policy.rego")
	if err != nil {
		t.Fatalf("reading policy.rego: %v", err)
	}
	text := string(source)

	for _, want := range []string{"package ecommerce.security", "import rego.v1", "default decision"} {
		if !strings.Contains(text, want) {
			t.Errorf("policy.rego does not contain %q", want)
		}
	}
	if strings.Contains(text, "package policy") {
		t.Error("policy.rego still contains a Go `package policy` clause")
	}
	if strings.Contains(text, "func ") {
		t.Error("policy.rego contains Go function declarations")
	}
}

func TestAuditLogRecordsDecisions(t *testing.T) {
	t.Parallel()

	e := NewPolicyEngine("rego", []string{string(DecisionActionPermission)})

	_, _ = e.Evaluate(DecisionActionPermission, input([]string{"catalog_admin"}, []string{"commerce"}, "read", "commerce"))
	_, _ = e.Evaluate(DecisionActionPermission, input(nil, nil, "read", "commerce"))

	entries := e.AuditLog()
	if len(entries) != 2 {
		t.Fatalf("AuditLog() has %d entries, want 2", len(entries))
	}
	if !entries[0].Allowed {
		t.Error("entry 0 should record an allow")
	}
	if entries[1].Allowed {
		t.Error("entry 1 should record a deny")
	}
}

func TestAuditLogIsACopy(t *testing.T) {
	t.Parallel()

	e := NewPolicyEngine("rego", nil)
	_, _ = e.Evaluate(DecisionResourceAccess, input([]string{"catalog_admin"}, []string{"commerce"}, "read", "commerce"))

	entries := e.AuditLog()
	entries[0].Allowed = false

	if !e.AuditLog()[0].Allowed {
		t.Error("AuditLog() leaked internal state")
	}
}

func TestDecisionPointsAreSortedCopies(t *testing.T) {
	t.Parallel()

	points := []string{"z", "a", "m"}
	e := NewPolicyEngine("rego", points)

	got := e.DecisionPoints()
	if len(got) != 3 || got[0] != "a" || got[2] != "z" {
		t.Errorf("DecisionPoints() = %v, want [a m z]", got)
	}

	got[0] = "tampered"
	if e.DecisionPoints()[0] != "a" {
		t.Error("DecisionPoints() leaked its internal slice")
	}
	if points[0] != "z" {
		t.Error("NewPolicyEngine sorted the caller's slice in place")
	}
}

func TestStrictToggle(t *testing.T) {
	t.Parallel()

	e := NewPolicyEngine("rego", nil)
	e.SetStrict(false)

	d, err := e.Evaluate(DecisionResourceAccess, input(nil, nil, "read", "commerce"))
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if d.Allow {
		t.Error("Evaluate() allowed a subject with no roles even with strict off")
	}
}

func TestMarshalDecision(t *testing.T) {
	t.Parallel()

	out, err := MarshalDecision(Decision{Allow: true, Reason: "ok"})
	if err != nil {
		t.Fatalf("MarshalDecision() error = %v", err)
	}

	var round Decision
	if err := json.Unmarshal(out, &round); err != nil {
		t.Fatalf("unmarshalling decision: %v", err)
	}
	if !round.Allow || round.Reason != "ok" {
		t.Errorf("round-tripped decision = %+v, want {true ok}", round)
	}
}
