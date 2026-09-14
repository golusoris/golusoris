#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2
#
# pre-commit: scan the staged diff for secrets with the repo's .gitleaks.toml.
set -euo pipefail
. "$(dirname "$0")/lib.sh"

need_tool gitleaks "https://github.com/gitleaks/gitleaks#installing (CI pins 8.30.1)"

cd "$(git rev-parse --show-toplevel)"
gitleaks git --pre-commit --staged --redact --no-banner -c .gitleaks.toml ||
  fail "gitleaks found a potential secret in the staged changes"
