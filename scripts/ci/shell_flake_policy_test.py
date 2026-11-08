# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Unit tests for shell flake policy covering early-exit readers and bare downloads."""

from __future__ import annotations

import tempfile
import unittest
from pathlib import Path

import shell_flake_policy


class ShellFlakePolicyTest(unittest.TestCase):
    """Exercise positive, negative, and boundary policy cases on file fixtures."""

    def setUp(self) -> None:
        """Create an isolated temporary directory for test fixtures."""
        self._temp_dir = tempfile.TemporaryDirectory(prefix="golusoris-flake-test-")
        self.temp_root = Path(self._temp_dir.name)
        self.addCleanup(self._temp_dir.cleanup)

    def write_fixture(self, filename: str, content: str) -> Path:
        """Write content into a temporary fixture file."""
        file_path = self.temp_root / filename
        file_path.parent.mkdir(parents=True, exist_ok=True)
        file_path.write_text(content, encoding="utf-8")
        return file_path

    def assert_has_rule(
        self, findings: list[shell_flake_policy.Finding], rule: str
    ) -> None:
        """Assert that at least one finding has the expected rule name."""
        rules = [f.rule for f in findings]
        self.assertIn(rule, rules, f"expected rule {rule!r} in {rules!r}")

    def test_positive_reader_fix_forms(self) -> None:
        """Verify that all approved Part A fix forms pass with zero reader findings."""
        positive_script = (
            'out="$(producer)"; grep -q PAT <<<"$out"\n'
            'if out="$(producer)" && grep -q PAT <<<"$out"; then echo ok; fi\n'
            'if ! { out="$(producer)" && grep -q PAT <<<"$out"; }; then echo ok; fi\n'
            '{ out="$(producer)" && grep -Fq PAT <<<"$out"; } || { echo fail; }\n'
            'grep -q PAT <<<"$var"\n'
            "producer | sed -n '1,4p'\n"
            "producer | sed -n '1p'\n"
            "grep -m 40 -E PAT file.txt\n"
        )
        path = self.write_fixture("scripts/ci/positive.sh", positive_script)
        findings = shell_flake_policy.scan_file(str(path), check_download=False)
        self.assertEqual(findings, [])

    def test_positive_retry_wrapped_downloads(self) -> None:
        """Verify that retry-wrapped go module download forms pass cleanly."""
        positive_downloads = (
            'bash scripts/ci/retry.sh go install mvdan.cc/gofumpt@v1.0.0\n'
            'GOFLAGS=-mod=mod bash "$root/scripts/ci/retry.sh" go get foo bar\n'
            'bash scripts/ci/retry.sh go mod download foo@v1.0.0\n'
            'bash scripts/ci/retry.sh $(GO) install mvdan.cc/gofumpt@v1.0.0\n'
            'GOBIN="$tmp" bash "$root/scripts/ci/retry.sh" go install foo\n'
            'bash "$repo_root/scripts/ci/retry.sh" go mod download "$module@$ver"\n'
            'download="$(GOFLAGS=-mod=mod go mod download -json "$module@$ver")"\n'
        )
        path = self.write_fixture("scripts/ci/positive_dl.sh", positive_downloads)
        findings = shell_flake_policy.scan_file(str(path), check_download=True)
        self.assertEqual(findings, [])

    def test_negative_reader_flag_variants(self) -> None:
        """Verify negative early-exit reader flag variants are flagged."""
        flag_cases = [
            "-q",
            "-Fq",
            "-qE",
            "-Eq",
            "--quiet",
            "--silent",
            "-m 1",
            "--max-count=1",
            "-l",
        ]
        for flag in flag_cases:
            content = f"echo hello | grep {flag} hello\n"
            path = self.write_fixture(f"scripts/ci/reader_{flag.replace(' ', '_')}.sh", content)
            findings = shell_flake_policy.scan_file(str(path), check_download=False)
            self.assert_has_rule(findings, "early-exit-reader")

    def test_negative_reader_dev_null_and_head(self) -> None:
        """Verify dev-null redirection, head forms, and pipeline variants are flagged."""
        reader_cases = [
            "echo hello | grep x >/dev/null\n",
            "echo hello | head -n 5\n",
            "echo hello | head -1\n",
            "echo hello |& grep -q hello\n",
        ]
        for idx, content in enumerate(reader_cases):
            path = self.write_fixture(f"scripts/ci/reader_variant_{idx}.sh", content)
            findings = shell_flake_policy.scan_file(str(path), check_download=False)
            self.assert_has_rule(findings, "early-exit-reader")

    def test_negative_reader_continuation_splits(self) -> None:
        """Verify continuation lines across backslash or pipe are joined and flagged."""
        split_cases = [
            "echo hello | \\\n  grep -q hello\n",
            "echo hello |\n  grep -q hello\n",
        ]
        for idx, content in enumerate(split_cases):
            path = self.write_fixture(f"scripts/ci/continuation_{idx}.sh", content)
            findings = shell_flake_policy.scan_file(str(path), check_download=False)
            self.assert_has_rule(findings, "early-exit-reader")

    def test_negative_bare_downloads(self) -> None:
        """Verify bare go module download forms are flagged."""
        bare_cases = [
            ("bare_install", "go install mvdan.cc/gofumpt@v1.0.0\n"),
            ("gobin_install", "GOBIN=/tmp/bin go install mvdan.cc/gofumpt@v1.0.0\n"),
            ("goflags_get", "GOFLAGS=-mod=mod go get foo@v1.0.0\n"),
            ("mod_download_json", 'go mod download -json "$module@$ver"\n'),
            ("make_go_install", "$(GO) install mvdan.cc/gofumpt@v1.0.0\n"),
            ("subshell_download", 'res="$(go install mvdan.cc/gofumpt@v1.0.0)"\n'),
            ("retry_not_as_command", "echo retry.sh && go install mvdan.cc/gofumpt@v1.0.0\n"),
        ]
        for name, content in bare_cases:
            path = self.write_fixture(f"scripts/ci/{name}.sh", content)
            findings = shell_flake_policy.scan_file(str(path), check_download=True)
            self.assert_has_rule(findings, "bare-module-download")

    def test_boundary_cases(self) -> None:
        """Verify non-pipe, comment, non-download, and excluded boundaries pass cleanly."""
        boundary_script = (
            "false || grep -q hello file.txt\n"
            "# echo hello | grep -q hello\n"
            "# go install mvdan.cc/gofumpt@v1.0.0\n"
            "echo hello | grep -c hello\n"
            "echo hello | grep -o hello\n"
            "grep -q hello /tmp/file.txt\n"
            "go mod verify\n"
            "go mod tidy\n"
            "go build ./...\n"
            "go test ./...\n"
        )
        path = self.write_fixture("scripts/ci/boundary.sh", boundary_script)
        findings = shell_flake_policy.scan_file(str(path), check_download=True)
        self.assertEqual(findings, [])

    def test_boundary_test_scripts_excluded_from_downloads(self) -> None:
        """Verify that _test.sh files are excluded from download scanning."""
        test_script = "go install mvdan.cc/gofumpt@v1.0.0\n"
        path = self.write_fixture("scripts/ci/sample_test.sh", test_script)
        in_dl_scope = shell_flake_policy.is_in_download_scope(str(path), set())
        self.assertFalse(in_dl_scope)

    def test_boundary_praetor_files_skipped(self) -> None:
        """Verify praetor-managed files are skipped from repository scanning."""
        praetor_files = {".github/workflows/praetor-docs.yml"}
        in_scope = shell_flake_policy.is_in_repo_scope(
            ".github/workflows/praetor-docs.yml", praetor_files
        )
        self.assertFalse(in_scope)


if __name__ == "__main__":
    unittest.main()
