// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package trainerio

import "errors"

// ConstructTrainer normalizes package-specific options before construction.
func ConstructTrainer[O, T any](
	options O,
	normalize func(O) (O, error),
	build func(O) *T,
) (*T, error) {
	if normalize == nil || build == nil {
		return nil, errors.New("ai/tiny: trainer constructor callbacks required")
	}
	normalized, err := normalize(options)
	if err != nil {
		return nil, err
	}
	trainer := build(normalized)
	if trainer == nil {
		return nil, errors.New("ai/tiny: trainer constructor returned nil")
	}
	return trainer, nil
}
