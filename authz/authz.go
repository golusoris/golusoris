// Package authz provides RBAC/ABAC policy enforcement via
// [casbin/casbin/v2]. Exposed as an fx module; apps supply a policy
// adapter (file, Postgres, etc.) and get an [*Enforcer] back.
//
// The default model is a simple RBAC model (role-based access control):
// subject, object, action triplets. Apps that need ABAC or multi-tenancy
// can supply their own model string via [Options.Model].
//
// Usage:
//
//	fx.New(
//	    golusoris.Core,
//	    fx.Supply(authz.Options{
//	        Model:   authz.ModelRBAC,          // or custom DSL string
//	        Adapter: authz.NewFileAdapter("policy.csv"),
//	    }),
//	    authz.Module,
//	)
//
//	// Check permission:
//	ok, err := enforcer.Enforce("alice", "/admin", "GET")
package authz

import (
	"fmt"
	"log/slog"

	"github.com/casbin/casbin/v2"
	"github.com/casbin/casbin/v2/model"
	"github.com/casbin/casbin/v2/persist"
	fileadapter "github.com/casbin/casbin/v2/persist/file-adapter"
	"go.uber.org/fx"
)

// ModelRBAC is a standard RBAC model DSL.
const ModelRBAC = `
[request_definition]
r = sub, obj, act

[policy_definition]
p = sub, obj, act

[role_definition]
g = _, _

[policy_effect]
e = some(where (p.eft == allow))

[matchers]
m = g(r.sub, p.sub) && r.obj == p.obj && r.act == p.act
`

// ModelRBACWithDeny extends RBAC with explicit deny rules.
const ModelRBACWithDeny = `
[request_definition]
r = sub, obj, act

[policy_definition]
p = sub, obj, act, eft

[role_definition]
g = _, _

[policy_effect]
e = some(where (p.eft == allow)) && !some(where (p.eft == deny))

[matchers]
m = g(r.sub, p.sub) && r.obj == p.obj && r.act == p.act
`

// Enforcer wraps *casbin.Enforcer with context and logging.
type Enforcer struct {
	e      *casbin.Enforcer
	logger *slog.Logger
}

// Enforce returns true if sub may perform act on obj.
func (e *Enforcer) Enforce(sub, obj, act string) (bool, error) {
	ok, err := e.e.Enforce(sub, obj, act)
	if err != nil {
		return false, fmt.Errorf("authz: enforce: %w", err)
	}
	e.logger.Debug("authz: enforce",
		slog.String("sub", sub), slog.String("obj", obj),
		slog.String("act", act), slog.Bool("ok", ok),
	)
	return ok, nil
}

// AddRoleForUser assigns role to user. Changes are persisted via the adapter.
func (e *Enforcer) AddRoleForUser(user, role string) error {
	if _, err := e.e.AddRoleForUser(user, role); err != nil {
		return fmt.Errorf("authz: add role: %w", err)
	}
	return nil
}

// DeleteRoleForUser removes role from user.
func (e *Enforcer) DeleteRoleForUser(user, role string) error {
	if _, err := e.e.DeleteRoleForUser(user, role); err != nil {
		return fmt.Errorf("authz: delete role: %w", err)
	}
	return nil
}

// GetRolesForUser returns all roles assigned to user.
func (e *Enforcer) GetRolesForUser(user string) ([]string, error) {
	roles, err := e.e.GetRolesForUser(user)
	if err != nil {
		return nil, fmt.Errorf("authz: get roles: %w", err)
	}
	return roles, nil
}

// Options configure the enforcer. Supply via fx.Supply or fx.Provide.
type Options struct {
	// Model is a Casbin model DSL string. Default [ModelRBAC].
	Model string
	// Adapter is the policy adapter. Supply a file adapter, a Postgres
	// adapter, or an in-memory adapter. Required.
	Adapter persist.Adapter
}

func newEnforcer(opts Options, logger *slog.Logger) (*Enforcer, error) {
	if opts.Model == "" {
		opts.Model = ModelRBAC
	}
	if opts.Adapter == nil {
		return nil, fmt.Errorf("authz: Options.Adapter is required")
	}
	m, err := model.NewModelFromString(opts.Model)
	if err != nil {
		return nil, fmt.Errorf("authz: parse model: %w", err)
	}
	e, err := casbin.NewEnforcer(m, opts.Adapter)
	if err != nil {
		return nil, fmt.Errorf("authz: new enforcer: %w", err)
	}
	logger.Info("authz: enforcer ready")
	return &Enforcer{e: e, logger: logger}, nil
}

// Module provides *authz.Enforcer. Requires Options to be supplied
// externally via fx.Supply or fx.Provide.
var Module = fx.Module("golusoris.authz",
	fx.Provide(newEnforcer),
)

// NewFileAdapter returns a Casbin file adapter for the given CSV policy
// file. Useful for simple apps or tests.
func NewFileAdapter(path string) persist.Adapter {
	return fileadapter.NewAdapter(path)
}

// NewEnforcerForTest constructs an Enforcer directly without fx, using
// the default RBAC model and a file adapter at policyPath.
func NewEnforcerForTest(policyPath string, logger *slog.Logger) (*Enforcer, error) {
	return newEnforcer(Options{
		Model:   ModelRBAC,
		Adapter: NewFileAdapter(policyPath),
	}, logger)
}

