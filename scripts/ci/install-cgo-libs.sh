#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# Install the Debian headers the Linux cgo modules compile against: hw/udev
# links libudev; media/3d links OpenAL, Vorbis, GL and X11 (go-gl/glfw).

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly script_dir

exec bash "$script_dir/verify-system-packages.sh" --install \
	libudev-dev libopenal-dev libvorbis-dev libgl-dev libx11-dev \
	libxrandr-dev libxxf86vm-dev libxi-dev libxcursor-dev libxinerama-dev
