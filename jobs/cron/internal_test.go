// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package cron

import "testing"

type pointerArgs struct{}

func (*pointerArgs) Kind() string { return "cron-pointer-probe" }

func TestPeriodicConstructorNormalizesTypedNil(t *testing.T) {
	t.Parallel()

	constructor := periodicConstructor(func() *pointerArgs { return nil })
	args, opts := constructor()
	if args != nil || opts != nil {
		t.Fatalf("constructor result = (%v, %v), want (nil, nil)", args, opts)
	}
}
