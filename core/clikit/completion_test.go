// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package clikit_test

import (
	"bytes"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/golusoris/golusoris/core/clikit"
)

// complete runs cobra's __complete protocol, which every generated script
// calls, and returns the candidates and the trailing directive line.
func complete(t *testing.T, args ...string) ([]string, string) {
	t.Helper()
	root := buildTree(true)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs(append([]string{cobra.ShellCompRequestCmd}, args...))
	if err := root.Execute(); err != nil {
		t.Fatalf("__complete %q: %v", args, err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	return lines[:len(lines)-1], lines[len(lines)-1]
}

func names(candidates []string) []string {
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		name, _, _ := strings.Cut(c, "\t")
		out = append(out, name)
	}
	return out
}

func TestShells(t *testing.T) {
	t.Parallel()
	want := []clikit.Shell{clikit.Bash, clikit.Zsh, clikit.Fish, clikit.PowerShell}
	if got := clikit.Shells(); !slices.Equal(got, want) {
		t.Fatalf("Shells() = %v, want %v", got, want)
	}
}

func TestWriteCompletionEveryShell(t *testing.T) {
	t.Parallel()
	for _, shell := range clikit.Shells() {
		var buf bytes.Buffer
		if err := clikit.WriteCompletion(&buf, buildTree(true), shell); err != nil {
			t.Fatalf("%s: %v", shell, err)
		}
		script := buf.String()
		if !strings.Contains(script, "myapp") || !strings.Contains(script, cobra.ShellCompRequestCmd) {
			t.Errorf("%s script does not call myapp %s", shell, cobra.ShellCompRequestCmd)
		}
	}
}

func TestWriteCompletionHonoursDisableDescriptions(t *testing.T) {
	t.Parallel()
	root := buildTree(true)
	root.CompletionOptions.DisableDescriptions = true
	var buf bytes.Buffer
	if err := clikit.WriteCompletion(&buf, root, clikit.Bash); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), cobra.ShellCompNoDescRequestCmd) {
		t.Errorf("bash script ignores DisableDescriptions")
	}
}

func TestWriteCompletionRejects(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := clikit.WriteCompletion(&buf, buildTree(true), "tcsh"); !errors.Is(err, clikit.ErrUnknownShell) {
		t.Errorf("unknown shell: err = %v, want ErrUnknownShell", err)
	}
	if err := clikit.WriteCompletion(&buf, buildTree(true), ""); !errors.Is(err, clikit.ErrUnknownShell) {
		t.Errorf("empty shell: err = %v, want ErrUnknownShell", err)
	}
	if err := clikit.WriteCompletion(&buf, nil, clikit.Bash); err == nil {
		t.Error("nil root: err = nil")
	}
}

// TestEnumFlagCompletesValues pins the protocol answer the four scripts
// render: the declared values in order, file completion off.
func TestEnumFlagCompletesValues(t *testing.T) {
	t.Parallel()
	for _, path := range [][]string{{"serve"}, {"srv"}} {
		got, directive := complete(t, append(path, "--format", "")...)
		if !slices.Equal(got, []string{"json", "yaml", "text"}) {
			t.Errorf("%v --format: candidates %q", path, got)
		}
		want := ":" + strconv.Itoa(int(cobra.ShellCompDirectiveNoFileComp|cobra.ShellCompDirectiveKeepOrder))
		if directive != want {
			t.Errorf("%v --format: directive %q, want %q", path, directive, want)
		}
	}
}

func TestCompletionHidesHiddenCommandsAndFlags(t *testing.T) {
	t.Parallel()
	cmds, _ := complete(t, "")
	if got := names(cmds); !slices.Equal(got, []string{"completion", "db", "help", "serve"}) {
		t.Errorf("subcommands = %q", got)
	}
	flags, _ := complete(t, "serve", "--")
	got := names(flags)
	for _, hidden := range []string{"--debug-token", "--legacy"} {
		if slices.Contains(got, hidden) {
			t.Errorf("flag completion offers %s: %q", hidden, got)
		}
	}
	if !slices.Contains(got, "--format") || !slices.Contains(got, "--verbose") {
		t.Errorf("flag completion lacks --format/--verbose: %q", got)
	}
}

func TestEnumFlagInheritedAndSingleValue(t *testing.T) {
	t.Parallel()
	root := clikit.New("app", "x").Cobra()
	root.PersistentFlags().String("level", "info", "log level")
	child := clikit.Command("run", "x", clikit.WithRunE(noop))
	root.AddCommand(child)
	if err := clikit.EnumFlag(child, "level", "info"); err != nil {
		t.Fatalf("inherited single-value enum: %v", err)
	}
}

func TestEnumFlagRejects(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		flag   string
		values []string
	}{
		"unknown flag": {"nope", []string{"a"}},
		"no values":    {"mode", nil},
		"empty value":  {"mode", []string{"a", ""}},
		"whitespace":   {"mode", []string{"a b"}},
		"tab":          {"mode", []string{"a\tb"}},
		"duplicate":    {"mode", []string{"a", "a"}},
	}
	for name, tc := range cases {
		cmd := &cobra.Command{Use: "x"}
		cmd.Flags().String("mode", "", "")
		if err := clikit.EnumFlag(cmd, tc.flag, tc.values...); !errors.Is(err, clikit.ErrInvalidEnum) {
			t.Errorf("%s: err = %v, want ErrInvalidEnum", name, err)
		}
	}
	if err := clikit.EnumFlag(nil, "mode", "a"); !errors.Is(err, clikit.ErrInvalidEnum) {
		t.Errorf("nil command: err = %v", err)
	}
	cmd := &cobra.Command{Use: "x"}
	cmd.Flags().String("mode", "", "")
	if err := clikit.EnumFlag(cmd, "mode", "a"); err != nil {
		t.Fatal(err)
	}
	if err := clikit.EnumFlag(cmd, "mode", "b"); !errors.Is(err, clikit.ErrInvalidEnum) {
		t.Errorf("second registration: err = %v, want ErrInvalidEnum", err)
	}
}
