// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package golusoris_test

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	ciGoPackageCheckout = "Check out golusoris system-package script"
	ciGoPackageProvide  = "Provide system packages"
	ciGoPackageGuard    = "${{ inputs.system-packages != '' }}"
	ciGoPackageRunner   = `bash "$ci_root/scripts/ci/verify-system-packages.sh" "${mode[@]}" "${packages[@]}"`
	// d8252ce1 has these six jobs for both package steps and cached setup-go.
	ciGoExpectedJobs = "apidiff build gosec lint test vuln"
)

type ciGoWorkflow struct {
	Jobs map[string]ciGoJob `json:"jobs"`
}

type ciGoJob struct {
	Needs any            `json:"needs"`
	Steps []workflowStep `json:"steps"`
}

type workflowStep struct {
	Name string `json:"name"`
	Uses string `json:"uses"`
	If   string `json:"if"`
	Run  string `json:"run"`
	With struct {
		Cache               bool   `json:"cache"`
		CacheDependencyPath string `json:"cache-dependency-path"` //nolint:tagliatelle // WHY: GitHub Actions names this input with hyphens.
	} `json:"with"`
}

func ciGoCachePath(name string, job ciGoJob) (string, error) {
	var path string
	for _, step := range job.Steps {
		if !strings.HasPrefix(step.Uses, "actions/setup-go@") || !step.With.Cache {
			continue
		}
		if path != "" {
			return "", fmt.Errorf("reusable setup-go cache user %s is duplicated", name)
		}
		path = step.With.CacheDependencyPath
		if path == "" {
			return "", fmt.Errorf("setup-go cache user %s lacks a dependency path", name)
		}
	}
	return path, nil
}

func ciGoNeedsBuild(needs any) bool {
	switch value := needs.(type) {
	case string:
		return value == "build"
	case []any:
		return slices.Contains(value, any("build"))
	default:
		return false
	}
}

func ciGoCacheUsers(wf ciGoWorkflow) error {
	users := make(map[string]string, len(wf.Jobs))
	for name, job := range wf.Jobs {
		path, err := ciGoCachePath(name, job)
		if err != nil {
			return fmt.Errorf("check cache steps: %w", err)
		}
		if path != "" {
			users[name] = path
		}
	}
	if err := ciGoJobSet(users, "reusable setup-go cache users changed"); err != nil {
		return err
	}
	for name := range strings.FieldsSeq(ciGoExpectedJobs) {
		if users[name] != users["build"] {
			return errors.New("reusable setup-go cache users do not share one cache key")
		}
		if name != "build" && !ciGoNeedsBuild(wf.Jobs[name].Needs) {
			return fmt.Errorf("reusable cache reader %s does not depend on build", name)
		}
	}
	return nil
}

func ciGoJobSet[V any](jobs map[string]V, message string) error {
	names := make([]string, 0, len(jobs))
	for name := range jobs {
		names = append(names, name)
	}
	slices.Sort(names)
	expected := strings.Fields(ciGoExpectedJobs)
	if !slices.Equal(names, expected) {
		return fmt.Errorf("%s: %v, expected %v", message, names, expected)
	}
	return nil
}

func ciGoStepIndex(job ciGoJob, name string) int {
	return slices.IndexFunc(job.Steps, func(step workflowStep) bool { return step.Name == name })
}

func ciGoSystemStep(jobName string, step workflowStep) error {
	guard := ciGoPackageGuard
	if jobName == "test" {
		guard = "${{ inputs.system-packages != '' && runner.os == 'Linux' }}"
	}
	if step.If != guard {
		return fmt.Errorf("system-package step %s in %s has incorrect guard", step.Name, jobName)
	}
	if step.Name != ciGoPackageProvide {
		return nil
	}
	var runners int
	for line := range strings.SplitSeq(step.Run, "\n") {
		if line == ciGoPackageRunner {
			runners++
		}
	}
	if runners != 1 {
		return fmt.Errorf("system-package provider %s must run the verifier exactly once", jobName)
	}
	return nil
}

func ciGoSystemJob(name string, job ciGoJob) error {
	for _, step := range job.Steps {
		if step.Name != ciGoPackageCheckout && step.Name != ciGoPackageProvide {
			continue
		}
		if err := ciGoSystemStep(name, step); err != nil {
			return fmt.Errorf("check system-package steps: %w", err)
		}
	}
	checkout := ciGoStepIndex(job, ciGoPackageCheckout)
	provide := ciGoStepIndex(job, ciGoPackageProvide)
	if checkout >= 0 && provide >= 0 && checkout >= provide {
		return fmt.Errorf("system-package checkout in %s must precede provider", name)
	}
	return nil
}

func ciGoSystemPackages(wf ciGoWorkflow) error {
	checkouts := make(map[string]bool, len(wf.Jobs))
	providers := make(map[string]bool, len(wf.Jobs))
	for name, job := range wf.Jobs {
		if err := ciGoSystemJob(name, job); err != nil {
			return fmt.Errorf("check system-package job: %w", err)
		}
		if ciGoStepIndex(job, ciGoPackageCheckout) >= 0 {
			checkouts[name] = true
		}
		if ciGoStepIndex(job, ciGoPackageProvide) >= 0 {
			providers[name] = true
		}
	}
	if err := ciGoJobSet(checkouts, "system-package checkout jobs changed"); err != nil {
		return err
	}
	return ciGoJobSet(providers, "system-package provider jobs changed")
}

type ciGoPolicyCase struct {
	name   string
	job    string
	want   string
	mutate func(*ciGoJob)
}

func runCIGoPolicyCases(t *testing.T, cases []ciGoPolicyCase, check func(ciGoWorkflow) error) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkCIGoPolicyCase(t, tc, check)
		})
	}
}

func checkCIGoPolicyCase(t *testing.T, tc ciGoPolicyCase, check func(ciGoWorkflow) error) {
	t.Helper()
	wf, err := loadWorkflowAs[ciGoWorkflow](filepath.Join(workflowDir, "ci-go.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if tc.mutate != nil {
		job := wf.Jobs[tc.job]
		tc.mutate(&job)
		wf.Jobs[tc.job] = job
	}
	err = check(wf)
	if tc.want == "" {
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), tc.want) {
		t.Fatalf("got %v, want error containing %q", err, tc.want)
	}
}

func ciGoCachedStepIndex(job ciGoJob) int {
	return slices.IndexFunc(job.Steps, func(step workflowStep) bool {
		return strings.HasPrefix(step.Uses, "actions/setup-go@") && step.With.Cache
	})
}

func TestCIGoCacheUsers(t *testing.T) {
	t.Parallel()
	cases := []ciGoPolicyCase{
		{name: "real workflow"},
		{
			name: "reader without writer", job: "lint",
			want:   "reusable cache reader lint does not depend on build",
			mutate: func(job *ciGoJob) { job.Needs = nil },
		},
		{
			name: "dependency list", job: "lint",
			mutate: func(job *ciGoJob) { job.Needs = []any{"other", "build"} },
		},
		{
			name: "cache key divergence", job: "lint",
			want: "reusable setup-go cache users do not share one cache key",
			mutate: func(job *ciGoJob) {
				job.Steps[ciGoCachedStepIndex(*job)].With.CacheDependencyPath = "divergent-go.sum"
			},
		},
		{
			name: "duplicate cached setup", job: "build",
			want: "reusable setup-go cache user build is duplicated",
			mutate: func(job *ciGoJob) {
				job.Steps = append(job.Steps, job.Steps[ciGoCachedStepIndex(*job)])
			},
		},
		{
			name: "missing dependency path", job: "lint",
			want:   "setup-go cache user lint lacks a dependency path",
			mutate: func(job *ciGoJob) { job.Steps[ciGoCachedStepIndex(*job)].With.CacheDependencyPath = "" },
		},
		{
			name: "missing cache user", job: "lint", want: "reusable setup-go cache users changed",
			mutate: func(job *ciGoJob) { job.Steps[ciGoCachedStepIndex(*job)].With.Cache = false },
		},
		{
			name: "extra cache user in spectral", job: "spectral", want: "reusable setup-go cache users changed",
			mutate: func(job *ciGoJob) {
				index := slices.IndexFunc(job.Steps, func(step workflowStep) bool {
					return strings.HasPrefix(step.Uses, "actions/setup-go@")
				})
				job.Steps[index].With.Cache = true
				job.Steps[index].With.CacheDependencyPath = "go.sum"
			},
		},
	}
	runCIGoPolicyCases(t, cases, ciGoCacheUsers)
}

func TestCIGoSystemPackages(t *testing.T) {
	t.Parallel()
	cases := []ciGoPolicyCase{
		{name: "real workflow"},
		{
			name: "missing checkout", job: "lint", want: "system-package checkout jobs changed",
			mutate: func(job *ciGoJob) {
				index := ciGoStepIndex(*job, ciGoPackageCheckout)
				job.Steps = slices.Delete(job.Steps, index, index+1)
			},
		},
		{
			name: "missing provider", job: "lint", want: "system-package provider jobs changed",
			mutate: func(job *ciGoJob) {
				index := ciGoStepIndex(*job, ciGoPackageProvide)
				job.Steps = slices.Delete(job.Steps, index, index+1)
			},
		},
		{
			name: "provider without guard", job: "lint", want: "system-package step Provide system packages in lint has incorrect guard",
			mutate: func(job *ciGoJob) { job.Steps[ciGoStepIndex(*job, ciGoPackageProvide)].If = "" },
		},
		{
			name: "checkout without guard", job: "lint", want: "system-package step Check out golusoris system-package script in lint has incorrect guard",
			mutate: func(job *ciGoJob) { job.Steps[ciGoStepIndex(*job, ciGoPackageCheckout)].If = "" },
		},
		{
			name: "provider before checkout", job: "lint", want: "system-package checkout in lint must precede provider",
			mutate: func(job *ciGoJob) {
				checkout, provide := ciGoStepIndex(*job, ciGoPackageCheckout), ciGoStepIndex(*job, ciGoPackageProvide)
				job.Steps[checkout], job.Steps[provide] = job.Steps[provide], job.Steps[checkout]
			},
		},
		{
			name: "duplicate verifier line", job: "lint", want: "system-package provider lint must run the verifier exactly once",
			mutate: func(job *ciGoJob) {
				job.Steps[ciGoStepIndex(*job, ciGoPackageProvide)].Run += ciGoPackageRunner + "\n"
			},
		},
		{
			name: "missing verifier line", job: "lint", want: "system-package provider lint must run the verifier exactly once",
			mutate: func(job *ciGoJob) { job.Steps[ciGoStepIndex(*job, ciGoPackageProvide)].Run = "true\n" },
		},
		{
			name: "Linux guard removed", job: "test", want: "system-package step Provide system packages in test has incorrect guard",
			mutate: func(job *ciGoJob) { job.Steps[ciGoStepIndex(*job, ciGoPackageProvide)].If = ciGoPackageGuard },
		},
	}
	runCIGoPolicyCases(t, cases, ciGoSystemPackages)
}
