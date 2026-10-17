<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# ai/tpe

Tree-structured Parzen Estimator (TPE) for black-box hyperparameter optimization over mixed continuous and discrete parameter spaces.
Ported from VMAFx/vmafx `pkg/prefilter/tpe.go` at `73c76b24a`.

## Surface

| Symbol | Role |
| :--- | :--- |
| `Kind` | Dimension domain (`KindContinuous`, `KindFloat`, `KindInt`, `KindBool`, `KindEnum`) |
| `Dimension` | Bounded search axis (`Name`, `Kind`, `Lo`, `Hi`) |
| `Config` | Search hyperparameters (`StartupTrials`, `EICandidates`, `Gamma`, `PriorWeight`) |
| `DefaultConfig()` | Standard defaults (10 startup trials, 24 EI candidates, gamma 0.10, prior weight 1.0) |
| `NewSampler(dims, cfg, seed)` | Construct validated sampler |
| `(*Sampler).Suggest()` | Propose next parameter map |
| `(*Sampler).Observe(params, obj)` | Ingest evaluated trial result |
| `(*Sampler).Best()` | Return lowest objective observed |
| `(*Sampler).Trials()` | Total observation count |

## Invariants

- Bounded complexity: dimensions <= 64, startup trials <= 1000, EI candidates <= 256, observations <= 10000.
- Deterministic trajectory: identical seed produces bit-identical proposals across trials.
- Sequential use: `Sampler` not concurrency-safe; callers evaluate sequentially.
- Mathematical purity: inverse CDF via Acklam rational approximation refined by Halley step; log-sum-exp numerically stable.
