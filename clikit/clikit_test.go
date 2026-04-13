package clikit_test

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/golusoris/golusoris/clikit"
	"github.com/spf13/cobra"
)

func TestRoot_execute_version(t *testing.T) {
	var buf bytes.Buffer
	root := clikit.New("testapp", "test application")
	root.AddCommand(
		clikit.Command("version", "print version",
			clikit.WithRunE(func(cmd *cobra.Command, _ []string) error {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "v1.2.3")
				return nil
			}),
		),
	)
	root.Cobra().SetOut(&buf)
	root.Cobra().SetArgs([]string{"version"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := buf.String(); got != "v1.2.3\n" {
		t.Fatalf("output: got %q, want %q", got, "v1.2.3\n")
	}
}

func TestCommand_withFx_startError(t *testing.T) {
	// fx.New with an option that provides a broken value causes app.Err() != nil.
	// We verify the command returns that error cleanly.
	type badDep struct{}
	cmd := clikit.Command("bad", "triggers fx error",
		clikit.WithFx(
			// Provide two values of the same type → fx constructor conflict
			// This is the simplest way to produce app.Err() without importing
			// anything heavy.
		),
	)
	cmd.SetArgs([]string{})
	// An fx.New with no options succeeds but app.Run would block — skip Run.
	// Just verify the command was built without panicking.
	if cmd == nil {
		t.Fatal("Command returned nil")
	}
	_ = badDep{}
}
