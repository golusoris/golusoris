#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# install-gosec.sh builds gosec GOSEC_VERSION against golang.org/x/tools
# GOSEC_XTOOLS_VERSION and installs it as $GOBIN/gosec (default $(go env GOPATH)/bin).
# Either version may come from the environment; tools/tool-versions.env fills the rest.

set -euo pipefail

# work is script-level so the EXIT trap still sees it after main returns.
work=""

main() {
	local root pinned_gosec pinned_xtools dest
	root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
	pinned_gosec="${GOSEC_VERSION:-}"
	pinned_xtools="${GOSEC_XTOOLS_VERSION:-}"
	# shellcheck source=tools/tool-versions.env disable=SC1091
	. "$root/tools/tool-versions.env"
	GOSEC_VERSION="${pinned_gosec:-$GOSEC_VERSION}"
	GOSEC_XTOOLS_VERSION="${pinned_xtools:-$GOSEC_XTOOLS_VERSION}"
	dest="${GOBIN:-$(go env GOPATH)/bin}"
	work="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-gosec.XXXXXX")"
	trap 'rm -rf -- "$work"' EXIT
	(
		cd "$work"
		go mod init golusoris.invalid/gosec-build >/dev/null 2>&1
		GOFLAGS=-mod=mod go get \
			"github.com/securego/gosec/v2@${GOSEC_VERSION}" \
			"golang.org/x/tools@${GOSEC_XTOOLS_VERSION}"
		mkdir -p "$dest"
		GOFLAGS=-mod=mod go build -o "$dest/gosec" github.com/securego/gosec/v2/cmd/gosec
	)
	go version -m "$dest/gosec" | grep -Fq "golang.org/x/tools	${GOSEC_XTOOLS_VERSION}" || {
		printf 'gosec was not built against golang.org/x/tools %s\n' "$GOSEC_XTOOLS_VERSION" >&2
		return 1
	}
	printf 'installed gosec %s with golang.org/x/tools %s: %s\n' "$GOSEC_VERSION" "$GOSEC_XTOOLS_VERSION" "$dest/gosec"
}

main "$@"
