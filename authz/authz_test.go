package authz_test

import (
	"log/slog"
	"os"
	"testing"

	"github.com/golusoris/golusoris/authz"
)

const testPolicy = `
p, alice, /data, GET
p, bob, /data, GET
p, alice, /admin, GET
g, bob, alice
`

func newTestEnforcer(t *testing.T) *authz.Enforcer {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "policy*.csv")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	_, _ = f.WriteString(testPolicy)
	_ = f.Close()

	e, err := authz.NewEnforcerForTest(f.Name(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("enforcer: %v", err)
	}
	return e
}

func TestEnforceAllow(t *testing.T) {
	t.Parallel()
	e := newTestEnforcer(t)

	ok, err := e.Enforce("alice", "/data", "GET")
	if err != nil {
		t.Fatalf("Enforce: %v", err)
	}
	if !ok {
		t.Error("alice should be allowed GET /data")
	}
}

func TestEnforceDeny(t *testing.T) {
	t.Parallel()
	e := newTestEnforcer(t)

	ok, err := e.Enforce("charlie", "/data", "GET")
	if err != nil {
		t.Fatalf("Enforce: %v", err)
	}
	if ok {
		t.Error("charlie should be denied GET /data")
	}
}

func TestRoleInheritance(t *testing.T) {
	t.Parallel()
	e := newTestEnforcer(t)

	// bob inherits alice's roles via g — should access /admin.
	ok, err := e.Enforce("bob", "/admin", "GET")
	if err != nil {
		t.Fatalf("Enforce: %v", err)
	}
	if !ok {
		t.Error("bob should inherit alice's /admin access")
	}
}

func TestAddDeleteRoleForUser(t *testing.T) {
	t.Parallel()
	e := newTestEnforcer(t)

	if err := e.AddRoleForUser("charlie", "alice"); err != nil {
		t.Fatalf("AddRoleForUser: %v", err)
	}
	ok, err := e.Enforce("charlie", "/admin", "GET")
	if err != nil {
		t.Fatalf("Enforce after add: %v", err)
	}
	if !ok {
		t.Error("charlie should have admin access after role assignment")
	}

	if err := e.DeleteRoleForUser("charlie", "alice"); err != nil {
		t.Fatalf("DeleteRoleForUser: %v", err)
	}
	ok, err = e.Enforce("charlie", "/admin", "GET")
	if err != nil {
		t.Fatalf("Enforce after delete: %v", err)
	}
	if ok {
		t.Error("charlie should lose admin access after role removal")
	}
}

func TestGetRolesForUser(t *testing.T) {
	t.Parallel()
	e := newTestEnforcer(t)

	roles, err := e.GetRolesForUser("bob")
	if err != nil {
		t.Fatalf("GetRolesForUser: %v", err)
	}
	// bob has role alice (via g, bob, alice in testPolicy)
	if len(roles) == 0 {
		t.Error("expected bob to have at least one role")
	}
	found := false
	for _, r := range roles {
		if r == "alice" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected role 'alice' in %v", roles)
	}
}
