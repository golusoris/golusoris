# Changelog

## [0.10.0](https://github.com/golusoris/golusoris/compare/core/v0.9.2...core/v0.10.0) (2026-10-09)


### ⚠ BREAKING CHANGES

* catch up with current Praetor and close the bug-ledger findings ([#633](https://github.com/golusoris/golusoris/issues/633))

### Features

* **clikit:** generate shell completions and man pages from the command tree ([b364430](https://github.com/golusoris/golusoris/commit/b364430dcf7bab6e9c0383a84946d9bcb1bd2d8c))
* **codec/jcs:** add RFC 8785 JSON canonicalisation ([b364430](https://github.com/golusoris/golusoris/commit/b364430dcf7bab6e9c0383a84946d9bcb1bd2d8c))
* **core/config:** load Kubernetes secret directories and *_FILE env ([58020a6](https://github.com/golusoris/golusoris/commit/58020a652c1e1014d6e09cf62562eb4e71386351))
* **core/config:** load Kubernetes secret directories and *_FILE env indirection ([ea4d1a1](https://github.com/golusoris/golusoris/commit/ea4d1a1b2d6708b01217a92e18e6981c59d001f6))
* **core/retry:** shared retry with capped exponential backoff and ([58020a6](https://github.com/golusoris/golusoris/commit/58020a652c1e1014d6e09cf62562eb4e71386351))
* **core/retry:** shared retry with capped exponential backoff and jitter ([ea4d1a1](https://github.com/golusoris/golusoris/commit/ea4d1a1b2d6708b01217a92e18e6981c59d001f6))
* **core/tlsx:** reload TLS certificates from files on handshake ([16f8978](https://github.com/golusoris/golusoris/commit/16f89782e6b7a0ea7fdd104d1f444f72a9b91edc))
* **db/pgx:** CloudNativePG password and certificate files, read-only ([58020a6](https://github.com/golusoris/golusoris/commit/58020a652c1e1014d6e09cf62562eb4e71386351))
* **db/pgx:** CloudNativePG password and certificate files, read-only pool ([ea4d1a1](https://github.com/golusoris/golusoris/commit/ea4d1a1b2d6708b01217a92e18e6981c59d001f6))
* **db/timescale:** detect the edition and degrade TSL-only features ([58020a6](https://github.com/golusoris/golusoris/commit/58020a652c1e1014d6e09cf62562eb4e71386351))
* **db/timescale:** detect the edition and degrade TSL-only features ([ea4d1a1](https://github.com/golusoris/golusoris/commit/ea4d1a1b2d6708b01217a92e18e6981c59d001f6))
* **db:** Timescale edition detection, CloudNativePG credentials, secret-file config and Helm value schemas ([#696](https://github.com/golusoris/golusoris/issues/696)) ([58020a6](https://github.com/golusoris/golusoris/commit/58020a652c1e1014d6e09cf62562eb4e71386351))
* **deploy/helm:** add preStop drain and validated termination grace ([cfa22ae](https://github.com/golusoris/golusoris/commit/cfa22ae176f4dc366b97cc851eb1787a013ff194))
* **grpc:** client TLS, keepalive, and retry policy from config ([16f8978](https://github.com/golusoris/golusoris/commit/16f89782e6b7a0ea7fdd104d1f444f72a9b91edc))
* **grpc:** configurable keepalive, reloading mTLS, and readiness health ([16f8978](https://github.com/golusoris/golusoris/commit/16f89782e6b7a0ea7fdd104d1f444f72a9b91edc))
* **httpx:** file-based mTLS for the server, custom TLS for the client ([16f8978](https://github.com/golusoris/golusoris/commit/16f89782e6b7a0ea7fdd104d1f444f72a9b91edc))
* **jsonschema:** generate Helm values schemas from koanf config structs ([58020a6](https://github.com/golusoris/golusoris/commit/58020a652c1e1014d6e09cf62562eb4e71386351))
* **jsonschema:** generate Helm values schemas from koanf config structs ([ea4d1a1](https://github.com/golusoris/golusoris/commit/ea4d1a1b2d6708b01217a92e18e6981c59d001f6))
* **junit:** write JUnit XML reports for CI gates ([b364430](https://github.com/golusoris/golusoris/commit/b364430dcf7bab6e9c0383a84946d9bcb1bd2d8c))
* **k8s/health:** add opt-in readiness checks for Postgres, Redis and NATS ([cfa22ae](https://github.com/golusoris/golusoris/commit/cfa22ae176f4dc366b97cc851eb1787a013ff194))
* **k8s/health:** fail readiness and drain before servers stop ([cfa22ae](https://github.com/golusoris/golusoris/commit/cfa22ae176f4dc366b97cc851eb1787a013ff194))
* **observability:** add metricdef, one metric catalog for services and generators ([b9d3f5c](https://github.com/golusoris/golusoris/commit/b9d3f5cbe881a2c4dce02824ae64630f77f44dff))
* **observability:** build PrometheusRules with mandatory runbooks and SLO burn-rate alerts ([b9d3f5c](https://github.com/golusoris/golusoris/commit/b9d3f5cbe881a2c4dce02824ae64630f77f44dff))
* **observability:** generate Grafana dashboards from metricdef in Go ([b9d3f5c](https://github.com/golusoris/golusoris/commit/b9d3f5cbe881a2c4dce02824ae64630f77f44dff))
* **observability:** let metricdef describe external and summary metrics ([b9d3f5c](https://github.com/golusoris/golusoris/commit/b9d3f5cbe881a2c4dce02824ae64630f77f44dff))
* **otel:** serve OTel metrics on /metrics with trace exemplars ([b9d3f5c](https://github.com/golusoris/golusoris/commit/b9d3f5cbe881a2c4dce02824ae64630f77f44dff))
* **otel:** stamp trace IDs on slog records and bridge the injected logger ([b9d3f5c](https://github.com/golusoris/golusoris/commit/b9d3f5cbe881a2c4dce02824ae64630f77f44dff))
* **testutil:** add promcheck, failing tests when queries name unemitted metrics ([b9d3f5c](https://github.com/golusoris/golusoris/commit/b9d3f5cbe881a2c4dce02824ae64630f77f44dff))


### Bug Fixes

* catch up with current Praetor and close the bug-ledger findings ([#633](https://github.com/golusoris/golusoris/issues/633)) ([511c84d](https://github.com/golusoris/golusoris/commit/511c84df54d27ef5cb8e3ca1e8a0e67417d7ff70))
* **deploy:** filter shipped HTTP rules on the label otelhttp emits ([b9d3f5c](https://github.com/golusoris/golusoris/commit/b9d3f5cbe881a2c4dce02824ae64630f77f44dff))
* **deps:** move to Go 1.27.2 and golang.org/x/net v0.60.0 for the October security release ([#706](https://github.com/golusoris/golusoris/issues/706)) ([7077742](https://github.com/golusoris/golusoris/commit/7077742860f7943e836d283651f7cb350be01c6f))
* **deps:** update module github.com/lmittmann/tint to v1.2.1 ([#582](https://github.com/golusoris/golusoris/issues/582)) ([2cc0269](https://github.com/golusoris/golusoris/commit/2cc0269c16f980a858aa7daa2de9df2d6b64363c))
* **httpx:** keep http.route on OTel metrics behind request-replacing middleware ([b9d3f5c](https://github.com/golusoris/golusoris/commit/b9d3f5cbe881a2c4dce02824ae64630f77f44dff))
* **mcp:** stop the streamable-HTTP server cleanly while clients are connected ([#651](https://github.com/golusoris/golusoris/issues/651)) ([25fcf42](https://github.com/golusoris/golusoris/commit/25fcf427944f6acb0efff69243447332291c6526))

## [0.9.2](https://github.com/golusoris/golusoris/compare/core/v0.9.1...core/v0.9.2) (2026-09-15)


### Code Refactoring

* **otel,selfupdate,sockmap,astx:** reduce complexity below HISS-04 caps ([#536](https://github.com/golusoris/golusoris/issues/536)) ([9a6c96b](https://github.com/golusoris/golusoris/commit/9a6c96b736fca021e5ee66d3cda6d7a3e94bc217))

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
