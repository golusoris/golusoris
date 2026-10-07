// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package clikit

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"
)

// Shell names a shell that [WriteCompletion] can target.
type Shell string

// Supported completion shells.
const (
	Bash       Shell = "bash"
	Zsh        Shell = "zsh"
	Fish       Shell = "fish"
	PowerShell Shell = "powershell"
)

// enumAnnotation is the pflag annotation key that carries [EnumFlag] values to
// the man page renderer.
const enumAnnotation = "golusoris_clikit_enum"

// ErrUnknownShell is returned for a [Shell] outside [Shells].
var ErrUnknownShell = errors.New("clikit: unknown shell")

// ErrInvalidEnum is returned when [EnumFlag] gets no flag or unusable values.
var ErrInvalidEnum = errors.New("clikit: invalid enum flag")

// Shells returns every supported [Shell] in a stable order.
func Shells() []Shell { return []Shell{Bash, Zsh, Fish, PowerShell} }

// WriteCompletion writes root's completion script for shell to w. The scripts
// ask the program itself for candidates at completion time, so commands,
// aliases, hidden commands and [EnumFlag] values never drift from the tree.
func WriteCompletion(w io.Writer, root *cobra.Command, shell Shell) error {
	if root == nil {
		return errors.New("clikit: completion: nil root command")
	}
	desc := !root.CompletionOptions.DisableDescriptions
	var err error
	switch shell {
	case Bash:
		err = root.GenBashCompletionV2(w, desc)
	case Zsh:
		err = zshCompletion(w, root, desc)
	case Fish:
		err = root.GenFishCompletion(w, desc)
	case PowerShell:
		err = powerShellCompletion(w, root, desc)
	default:
		return fmt.Errorf("%w: %q", ErrUnknownShell, shell)
	}
	if err != nil {
		return fmt.Errorf("clikit: completion %s: %w", shell, err)
	}
	return nil
}

func zshCompletion(w io.Writer, root *cobra.Command, desc bool) error {
	if desc {
		return root.GenZshCompletion(w) //nolint:wrapcheck // wrapped by WriteCompletion
	}
	return root.GenZshCompletionNoDesc(w) //nolint:wrapcheck // wrapped by WriteCompletion
}

func powerShellCompletion(w io.Writer, root *cobra.Command, desc bool) error {
	if desc {
		return root.GenPowerShellCompletionWithDesc(w) //nolint:wrapcheck // wrapped by WriteCompletion
	}
	return root.GenPowerShellCompletion(w) //nolint:wrapcheck // wrapped by WriteCompletion
}

// completionPath is the conventional install location of a shell's script,
// relative and slash-separated.
func completionPath(name string, shell Shell) string {
	switch shell {
	case Zsh:
		return "completions/zsh/_" + name
	case Fish:
		return "completions/fish/" + name + ".fish"
	case PowerShell:
		return "completions/powershell/" + name + ".ps1"
	case Bash:
		return "completions/bash/" + name
	}
	return "completions/" + string(shell) + "/" + name
}

// EnumFlag declares the accepted values of the flag name visible to cmd
// (local or inherited): every shell completes exactly these values and the man
// page lists them. Values must be non-empty, unique, and free of whitespace.
func EnumFlag(cmd *cobra.Command, name string, values ...string) error {
	if cmd == nil {
		return fmt.Errorf("%w: nil command", ErrInvalidEnum)
	}
	if err := validateEnum(values); err != nil {
		return fmt.Errorf("%w: --%s: %w", ErrInvalidEnum, name, err)
	}
	flag := cmd.Flag(name)
	if flag == nil {
		return fmt.Errorf("%w: --%s: flag not defined on %q", ErrInvalidEnum, name, cmd.CommandPath())
	}
	choices := slices.Clone(values)
	directive := cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveKeepOrder
	if err := cmd.RegisterFlagCompletionFunc(name, cobra.FixedCompletions(choices, directive)); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidEnum, err)
	}
	if flag.Annotations == nil {
		flag.Annotations = map[string][]string{}
	}
	flag.Annotations[enumAnnotation] = slices.Clone(values)
	return nil
}

func validateEnum(values []string) error {
	if len(values) == 0 {
		return errors.New("no values")
	}
	seen := make(map[string]struct{}, len(values))
	for _, v := range values {
		if v == "" || strings.ContainsAny(v, " \t\r\n") {
			return fmt.Errorf("value %q is empty or holds whitespace", v)
		}
		if _, dup := seen[v]; dup {
			return fmt.Errorf("value %q repeats", v)
		}
		seen[v] = struct{}{}
	}
	return nil
}
