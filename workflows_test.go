// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package golusoris_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

const workflowDir = ".github/workflows"

// permission levels in GitHub's order: a write grant covers a read request.
const (
	permNone = iota
	permRead
	permWrite
)

var permLevels = map[string]int{"none": permNone, "read": permRead, "write": permWrite}

// tokenPerms is a GITHUB_TOKEN grant: named scopes, and the level of every
// scope it does not name (read-all, write-all, or none for a mapping).
type tokenPerms struct {
	scopes map[string]int
	rest   int
}

func (p tokenPerms) level(scope string) int {
	if l, ok := p.scopes[scope]; ok {
		return l
	}
	return p.rest
}

// restrictedDefault is the grant a workflow without any permissions key gets
// under the organisation's restricted default.
var restrictedDefault = tokenPerms{scopes: map[string]int{"contents": permRead, "packages": permRead}}

type workflowJob struct {
	Uses        string `json:"uses"`
	Permissions any    `json:"permissions"`
}

type workflowFile struct {
	Permissions any                    `json:"permissions"`
	Jobs        map[string]workflowJob `json:"jobs"`
}

func parsePerms(raw any) (tokenPerms, error) {
	switch v := raw.(type) {
	case string:
		switch v {
		case "read-all":
			return tokenPerms{rest: permRead}, nil
		case "write-all":
			return tokenPerms{rest: permWrite}, nil
		}
		return tokenPerms{}, fmt.Errorf("unknown permissions shorthand %q", v)
	case map[string]any:
		p := tokenPerms{scopes: make(map[string]int, len(v))}
		for scope, lv := range v {
			s, _ := lv.(string)
			l, ok := permLevels[s]
			if !ok {
				return tokenPerms{}, fmt.Errorf("scope %s: unknown level %v", scope, lv)
			}
			p.scopes[scope] = l
		}
		return p, nil
	}
	return tokenPerms{}, fmt.Errorf("permissions of type %T", raw)
}

// effectivePerms resolves a job's grant: its own key, else the workflow's,
// else fallback.
func effectivePerms(job, workflow any, fallback tokenPerms) (tokenPerms, error) {
	for _, raw := range []any{job, workflow} {
		if raw != nil {
			return parsePerms(raw)
		}
	}
	return fallback, nil
}

func loadWorkflow(path string) (workflowFile, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: test reads the repository's own workflow files
	if err != nil {
		return workflowFile{}, fmt.Errorf("read %s: %w", path, err)
	}
	var wf workflowFile
	if err := yaml.Unmarshal(data, &wf); err != nil {
		return workflowFile{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return wf, nil
}

// calleeNeeds is the highest grant any job of a called workflow requests.
// A job without its own or a workflow-level key inherits the caller's grant
// and so requests nothing beyond it.
func calleeNeeds(callee workflowFile) (tokenPerms, error) {
	need := tokenPerms{scopes: map[string]int{}}
	for name, job := range callee.Jobs {
		p, err := effectivePerms(job.Permissions, callee.Permissions, tokenPerms{})
		if err != nil {
			return tokenPerms{}, fmt.Errorf("job %s: %w", name, err)
		}
		need.rest = max(need.rest, p.rest)
		for scope, l := range p.scopes {
			need.scopes[scope] = max(need.scopes[scope], l)
		}
	}
	return need, nil
}

// missingGrants lists every scope need requests above what grant gives.
func missingGrants(grant, need tokenPerms) []string {
	var missing []string
	if need.rest > grant.rest {
		missing = append(missing, fmt.Sprintf("every scope at level %d", need.rest))
	}
	for scope, l := range need.scopes {
		if l > grant.level(scope) {
			missing = append(missing, scope)
		}
	}
	slices.Sort(missing)
	return missing
}

// callViolations checks every job of one workflow that calls a local
// reusable workflow; GitHub refuses to start a run whose callee asks for more
// than the calling job holds (startup_failure).
func callViolations(root, name string) ([]string, error) {
	wf, err := loadWorkflow(filepath.Join(root, workflowDir, name))
	if err != nil {
		return nil, err
	}
	var out []string
	for jobName, job := range wf.Jobs {
		target, ok := strings.CutPrefix(job.Uses, "./")
		if !ok {
			continue
		}
		grant, err := effectivePerms(job.Permissions, wf.Permissions, restrictedDefault)
		if err != nil {
			return nil, fmt.Errorf("%s job %s: %w", name, jobName, err)
		}
		callee, err := loadWorkflow(filepath.Join(root, target))
		if err != nil {
			return nil, err
		}
		need, err := calleeNeeds(callee)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", target, err)
		}
		if missing := missingGrants(grant, need); len(missing) > 0 {
			out = append(out, fmt.Sprintf("%s job %s calls %s without %s", name, jobName, target, strings.Join(missing, ", ")))
		}
	}
	return out, nil
}

func reusableWorkflowViolations(root string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, workflowDir))
	if err != nil {
		return nil, fmt.Errorf("list workflows: %w", err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || (filepath.Ext(e.Name()) != ".yml" && filepath.Ext(e.Name()) != ".yaml") {
			continue
		}
		v, err := callViolations(root, e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, v...)
	}
	slices.Sort(out)
	return out, nil
}

// TestReusableWorkflowPermissions keeps every local reusable-workflow call
// startable: v0.13.0's release and SBOM runs failed at startup because the
// release gate asked for checks: read that its callers did not grant.
func TestReusableWorkflowPermissions(t *testing.T) {
	t.Parallel()
	got, err := reusableWorkflowViolations(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range got {
		t.Error(v)
	}
}

func writeWorkflows(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, workflowDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const gateCallee = `
on: {workflow_call: {}}
permissions: {contents: read}
jobs:
  gate:
    permissions: {contents: read, checks: read}
    runs-on: ubuntu-24.04
    steps: [{run: "true"}]
`

func TestReusableWorkflowPermissionsFixtures(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		caller string
		want   []string
	}{
		"caller lacks checks": {
			caller: "permissions: {contents: read}\njobs:\n  gate: {uses: ./.github/workflows/gate.yml}\n",
			want:   []string{"caller.yml job gate calls .github/workflows/gate.yml without checks"},
		},
		"job grant covers callee": {
			caller: "permissions: {contents: read}\njobs:\n  gate:\n    uses: ./.github/workflows/gate.yml\n    permissions: {contents: read, checks: read}\n",
		},
		"write covers read": {
			caller: "permissions: {contents: write, checks: write}\njobs:\n  gate: {uses: ./.github/workflows/gate.yml}\n",
		},
		"read-all covers read": {
			caller: "permissions: read-all\njobs:\n  gate: {uses: ./.github/workflows/gate.yml}\n",
		},
		"job grant narrows workflow grant": {
			caller: "permissions: write-all\njobs:\n  gate:\n    uses: ./.github/workflows/gate.yml\n    permissions: {}\n",
			want:   []string{"caller.yml job gate calls .github/workflows/gate.yml without checks, contents"},
		},
		"unset falls back to restricted default": {
			caller: "jobs:\n  gate: {uses: ./.github/workflows/gate.yml}\n",
			want:   []string{"caller.yml job gate calls .github/workflows/gate.yml without checks"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := writeWorkflows(t, map[string]string{"caller.yml": tc.caller, "gate.yml": gateCallee})
			got, err := reusableWorkflowViolations(root)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("violations = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReusableWorkflowPermissionsRejectsUnknownLevel(t *testing.T) {
	t.Parallel()
	root := writeWorkflows(t, map[string]string{
		"caller.yml": "permissions: {contents: maybe}\njobs:\n  gate: {uses: ./.github/workflows/gate.yml}\n",
		"gate.yml":   gateCallee,
	})
	if _, err := reusableWorkflowViolations(root); err == nil || !strings.Contains(err.Error(), "unknown level") {
		t.Fatalf("err = %v, want unknown level", err)
	}
}
