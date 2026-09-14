// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package crypto

import "go.uber.org/fx"

// Module provides the config-driven helpers — a *PasswordHasher (load-shed
// bounded) and an *Encryptor (key resolved from config). Both are lazy: an app
// that doesn't depend on them constructs neither. The argon2id / AES-GCM
// functions are stateless and usable without the module.
var Module = fx.Module(
	"golusoris.crypto",
	fx.Provide(newPasswordHasher),
	fx.Provide(newEncryptor),
)
