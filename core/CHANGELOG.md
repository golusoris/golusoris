# Changelog

## [0.9.1](https://github.com/golusoris/golusoris/compare/core/v0.9.0...core/v0.9.1) (2026-09-14)


### Bug Fixes

* **governance:** make the praetor audit pass (devcontainer, HISS-01 recursion) ([#493](https://github.com/golusoris/golusoris/issues/493)) ([dfb3356](https://github.com/golusoris/golusoris/commit/dfb3356206b8d7eb93dafae3e49526fa355b7a6a))


### Code Refactoring

* **governance:** move lint and gosec configs to praetor's canonical paths ([#494](https://github.com/golusoris/golusoris/issues/494)) ([8dad6ef](https://github.com/golusoris/golusoris/commit/8dad6ef7d3770e3bb02857506449ae22be6a7ea6))

## [0.9.0](https://github.com/golusoris/golusoris/compare/core/v0.8.0...core/v0.9.0) (2026-09-14)


### ⚠ BREAKING CHANGES

* **core:** HISS-07/02 burn-down for codec/yaml, config, id, mcp ([#474](https://github.com/golusoris/golusoris/issues/474))
* **core:** the ten core packages move to github.com/golusoris/golusoris/core/...; the Go toolchain floor rises to 1.27.0; the licence changes from MIT to EUPL-1.2 from this release on.

### Features

* **core:** lean core sub-module, capability contract, EUPL-1.2, praetor governance ([#447](https://github.com/golusoris/golusoris/issues/447)) ([f81b1d9](https://github.com/golusoris/golusoris/commit/f81b1d988662dd055cd69836fd319da04f36e21f))


### Code Refactoring

* clear the five HISS-19 duplicate blocks on main and pin the storage path guard ([#476](https://github.com/golusoris/golusoris/issues/476)) ([16db9b0](https://github.com/golusoris/golusoris/commit/16db9b08d3c67b5c4d41dcb8bfff39ef77eb6a24))
* **core:** HISS-07/02 burn-down for codec/yaml, config, id, mcp ([#474](https://github.com/golusoris/golusoris/issues/474)) ([c0fd1c3](https://github.com/golusoris/golusoris/commit/c0fd1c3d8f3da29e11a7195d04301fbc35c02042))
