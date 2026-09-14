// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package receipt

import (
	"encoding/hex"
	"fmt"
	"log/slog"

	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
)

// SeedKey is the config key holding the hex-encoded 32-byte Ed25519 seed.
const SeedKey = "crypto.receipt.seed"

// Module provides a *Signer. The key comes from SeedKey; when unset an
// ephemeral key is generated and logged loudly — fine for local runs, useless
// for attestations anyone else must verify.
var Module = fx.Module(
	"golusoris.crypto.receipt",
	fx.Provide(newSigner),
)

func newSigner(cfg *config.Config, clk clock.Clock, logger *slog.Logger) (*Signer, error) {
	if seedHex := cfg.String(SeedKey); seedHex != "" {
		seed, err := hex.DecodeString(seedHex)
		if err != nil {
			return nil, fmt.Errorf("receipt: decode %s: %w", SeedKey, err)
		}
		return NewSignerFromSeed(seed, clk)
	}
	_, priv, err := GenerateKey()
	if err != nil {
		return nil, err
	}
	logger.Warn("receipt: no seed configured — using an ephemeral signing key; receipts cannot be verified by other parties", slog.String("config_key", SeedKey))
	return NewSigner(priv, clk)
}
