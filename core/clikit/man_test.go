// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package clikit_test

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/golusoris/golusoris/core/clikit"
)

func manPages(t *testing.T, root *cobra.Command, opts clikit.ManOptions) map[string]string {
	t.Helper()
	pages, err := clikit.ManPages(root, opts)
	if err != nil {
		t.Fatalf("ManPages: %v", err)
	}
	out := make(map[string]string, len(pages))
	for name, data := range pages {
		out[name] = string(data)
	}
	return out
}

func TestManPagesContent(t *testing.T) {
	t.Parallel()
	pages := manPages(t, buildTree(true), clikit.ManOptions{})
	serve := pages["man/man1/myapp-serve.1"]
	for _, want := range []string{
		`.TH "MYAPP\-SERVE" "1" "" "" ""`,
		".SH ALIASES\nsrv\n",
		"Values: json, yaml, text\n",
		`\fB\-f\fR, \fB\-\-format\fR \fIformat\fR`,
		"output format\n",
		`Default: \(dqjson\(dq`,
		".SH \"OPTIONS INHERITED FROM PARENT COMMANDS\"\n",
		`\fBmyapp\fR(1)`,
	} {
		if !strings.Contains(serve, want) {
			t.Errorf("serve page lacks %q:\n%s", want, serve)
		}
	}
	for _, banned := range []string{"debug", "legacy", "internal"} {
		for name, page := range pages {
			if strings.Contains(page, banned) {
				t.Errorf("%s mentions hidden or deprecated %q", name, banned)
			}
		}
	}
}

func TestManPagesEscapesRoff(t *testing.T) {
	t.Parallel()
	serve := manPages(t, buildTree(true), clikit.ManOptions{})["man/man1/myapp-serve.1"]
	for _, want := range []string{`\&.dot\-led line`, `back\eslash`, `\(aqquotes\(aq`, `\-\- dashes`, ".sp\n"} {
		if !strings.Contains(serve, want) {
			t.Errorf("serve page lacks escaped %q", want)
		}
	}
}

func TestManPagesOptions(t *testing.T) {
	t.Parallel()
	opts := clikit.ManOptions{Section: "8", Date: "2026-10", Source: `my"app 1.0`, Manual: "Ops"}
	pages := manPages(t, buildTree(true), opts)
	page, ok := pages["man/man8/myapp.8"]
	if !ok {
		t.Fatalf("section 8 page missing; got %q", slices.Sorted(maps.Keys(pages)))
	}
	if !strings.Contains(page, `.TH "MYAPP" "8" "2026\-10" "my\(dqapp 1.0" "Ops"`) {
		t.Errorf("title line not filled from options:\n%s", page)
	}
	if !strings.Contains(page, `\fBmyapp\-serve\fR(8)`) {
		t.Errorf("SEE ALSO ignores section:\n%s", page)
	}
}

func TestManPagesSingleCommand(t *testing.T) {
	t.Parallel()
	pages := manPages(t, &cobra.Command{Use: "solo", Short: "one"}, clikit.ManOptions{})
	if got := slices.Sorted(maps.Keys(pages)); !slices.Equal(got, []string{"man/man1/solo.1"}) {
		t.Fatalf("pages = %q", got)
	}
	if strings.Contains(pages["man/man1/solo.1"], "SEE ALSO") {
		t.Error("lone command page has a SEE ALSO section")
	}
}

func TestManPagesRejects(t *testing.T) {
	t.Parallel()
	if _, err := clikit.ManPages(nil, clikit.ManOptions{}); err == nil {
		t.Error("nil root: err = nil")
	}
	for _, tc := range []struct {
		children int
		wantErr  bool
	}{{clikit.MaxCommands - 1, false}, {clikit.MaxCommands, true}} {
		root := &cobra.Command{Use: "big"}
		root.CompletionOptions.DisableDefaultCmd = true
		for i := range tc.children {
			root.AddCommand(&cobra.Command{Use: "c" + strconv.Itoa(i), Run: func(*cobra.Command, []string) {}})
		}
		_, err := clikit.ManPages(root, clikit.ManOptions{})
		if got := errors.Is(err, clikit.ErrTooManyCommands); got != tc.wantErr {
			t.Errorf("%d commands: err = %v, want ErrTooManyCommands=%v", tc.children+1, err, tc.wantErr)
		}
	}
}

// TestManPagesGroffClean renders every page with groff's warnings on; the
// planted page first proves this groff reports what the check looks for.
func TestManPagesGroffClean(t *testing.T) {
	t.Parallel()
	groff, err := exec.LookPath("groff")
	if testing.Short() || err != nil {
		t.Skip("needs groff on PATH and no -short")
	}
	if msg := groffWarnings(t, groff, []byte(".TH A 1\n.SH NAME\na \\- b\n.dot line\nback\\slash\n")); msg == "" {
		t.Fatal("groff stayed silent on a page with an unknown request and a bad escape")
	}
	for name, page := range manPages(t, buildTree(true), clikit.ManOptions{Source: "myapp 1.0.0"}) {
		if msg := groffWarnings(t, groff, []byte(page)); msg != "" {
			t.Errorf("%s: groff warns:\n%s", name, msg)
		}
	}
}

func groffWarnings(t *testing.T, groff string, page []byte) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), shellTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, groff, "-man", "-ww", "-z")
	cmd.Stdin = bytes.NewReader(page)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("groff: %v\n%s", err, stderr.String())
	}
	return stderr.String()
}
