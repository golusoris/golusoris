//go:build !linux || !cgo

// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package udev

import "context"

// NewMonitor reports [ErrUnsupported] when libudev cannot be linked.
func NewMonitor(context.Context) (*Monitor, error) {
	return nil, ErrUnsupported
}
