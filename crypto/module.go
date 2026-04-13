package crypto

import "go.uber.org/fx"

// Module exposes nothing constructed (the package is a stateless toolkit) —
// it exists for symmetry with other golusoris modules and so that golusoris.Core
// can include it without special-casing.
var Module = fx.Module("golusoris.crypto")
