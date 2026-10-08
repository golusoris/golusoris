// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package clikit_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/golusoris/golusoris/core/clikit"
)

// shellTimeout bounds one shell run (HISS-02).
const shellTimeout = 30 * time.Second

// harnesses drive each generated script outside an interactive session and
// print the candidates for `myapp serve --format <TAB>`. bash and zsh get the
// minimal stand-ins for the completion-system functions the scripts call.
var harnesses = map[clikit.Shell]struct {
	bin  string
	args []string
	body string
}{
	clikit.Bash: {"bash", []string{"--noprofile", "--norc", "-c"}, `
source "$1"
_get_comp_words_by_ref() { cur=${COMP_WORDS[COMP_CWORD]}; prev=${COMP_WORDS[COMP_CWORD-1]}; words=("${COMP_WORDS[@]}"); cword=$COMP_CWORD; }
compopt() { :; }
COMP_WORDS=(myapp serve --format "")
COMP_CWORD=3
COMP_LINE="myapp serve --format "
COMP_POINT=${#COMP_LINE}
__start_myapp
printf '%s\n' "${COMPREPLY[@]}"
`},
	clikit.Zsh: {"zsh", []string{"-f", "-c"}, `
compdef() { : }
_describe() { print -rl -- "${completions[@]}" }
_arguments() { : }
compadd() { : }
source "$1"
words=(myapp serve --format "")
CURRENT=4
_myapp
`},
	clikit.Fish: {"fish", []string{"--no-config", "-c"}, `
source $argv[-1]
complete -C "myapp serve --format "
`},
}

// TestShellHarnesses is the #630 acceptance: the enumerated flag completes
// its values in each installed shell. PowerShell has no harness here: pwsh is
// rarely installed on the CI hosts; its script speaks the same __complete
// protocol pinned by TestEnumFlagCompletesValues.
func TestShellHarnesses(t *testing.T) {
	t.Parallel()
	if testing.Short() || runtime.GOOS == "windows" {
		t.Skip("spawns real shells; skipped under -short and on Windows (symlinked helper binary)")
	}
	bin := helperBin(t)
	for shell, h := range harnesses {
		t.Run(string(shell), func(t *testing.T) {
			t.Parallel()
			path, err := exec.LookPath(h.bin)
			if err != nil {
				t.Skipf("%s not installed", h.bin)
			}
			script := filepath.Join(t.TempDir(), "completion")
			var buf bytes.Buffer
			if err := clikit.WriteCompletion(&buf, buildTree(true), shell); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(script, buf.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			got := runShell(t, bin, path, append(slices.Clone(h.args), h.body, "harness", script))
			for _, want := range []string{"json", "yaml", "text"} {
				if !slices.Contains(got, want) {
					t.Errorf("%s offered %q, want it to contain %q", shell, got, want)
				}
			}
			if slices.Contains(got, "--port") {
				t.Errorf("%s offered flags instead of values: %q", shell, got)
			}
		})
	}
}

// helperBin links the test binary as "myapp" in a fresh PATH directory.
func helperBin(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Symlink(exe, filepath.Join(dir, "myapp")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	return dir
}

func runShell(t *testing.T, binDir, shell string, args []string) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), shellTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, args...)
	cmd.Env = append(os.Environ(), helperEnv+"=1", "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s: %v\nstderr:\n%s", shell, err, stderr.String())
	}
	var out []string
	for line := range strings.SplitSeq(stdout.String(), "\n") {
		if name, _, _ := strings.Cut(line, "\t"); strings.TrimSpace(name) != "" {
			out = append(out, strings.TrimSpace(name))
		}
	}
	return out
}
