<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# ADR-0026: Keep hand-written LLM provider clients

- **Status**: Accepted
- **Date**: 2026-10-29
- **Deciders**: @lusoris
- **Tags**: `ai`, `llm`, `dependencies`

## Context

The packages `ai/llm`, `ai/llm/anthropic`, and `ai/llm/ollama` provide minimal, resilient HTTP clients for OpenAI-compatible endpoints, Anthropic Messages, and Ollama APIs. The public interface defines a text-only contract centered on `Client` with `Chat`, `Stream`, and `Embed` methods ([`ai/llm/llm.go:263`](https://github.com/golusoris/golusoris/blob/main/ai/llm/llm.go#L263)). The underlying types strictly exchange text: `Message` ([`ai/llm/llm.go:59`](https://github.com/golusoris/golusoris/blob/main/ai/llm/llm.go#L59)), `Response` ([`ai/llm/llm.go:65`](https://github.com/golusoris/golusoris/blob/main/ai/llm/llm.go#L65)), and `Chunk` ([`ai/llm/llm.go:73`](https://github.com/golusoris/golusoris/blob/main/ai/llm/llm.go#L73)).

The current in-tree provider wire and mapping slices comprise 642 physical lines of Go across three packages:
- OpenAI wire client and mapping: 250 lines ([`ai/llm/llm.go:321-570`](https://github.com/golusoris/golusoris/blob/main/ai/llm/llm.go#L321-L570)).
- Anthropic wire client and mapping: 198 lines ([`ai/llm/anthropic/anthropic.go:115-312`](https://github.com/golusoris/golusoris/blob/main/ai/llm/anthropic/anthropic.go#L115-L312)).
- Ollama wire client and mapping: 194 lines ([`ai/llm/ollama/ollama.go:101-294`](https://github.com/golusoris/golusoris/blob/main/ai/llm/ollama/ollama.go#L101-L294)).
- Shared SSE line scanner: 29 lines ([`ai/llm/llm.go:188-216`](https://github.com/golusoris/golusoris/blob/main/ai/llm/llm.go#L188-L216)).

Key operational invariants enforced by the hand-written clients:
- **Strict byte and timeout bounds**: `HTTPBounds` ([`ai/llm/llm.go:106`](https://github.com/golusoris/golusoris/blob/main/ai/llm/llm.go#L106)) default to 120s timeout, 4 MiB response limit, 64 KiB error body limit, and 1 MiB stream frame limit, rejecting unbounded configurations and sentinel overflow with `httpx/client.ReadAllBounded`.
- **Strict stream termination**: Streams must terminate cleanly with provider sentinels (`[DONE]` for OpenAI, `message_stop` for Anthropic, `done: true` for Ollama). Truncated connections or premature EOF before the sentinel produce explicit `ErrStreamTruncated` errors rather than silent partial success, validated by regression suites ([`ai/llm/http_boundary_test.go`](https://github.com/golusoris/golusoris/blob/main/ai/llm/http_boundary_test.go), [`ai/llm/runstream_test.go`](https://github.com/golusoris/golusoris/blob/main/ai/llm/runstream_test.go)).
- **Close-error preservation**: Body closure errors are captured via `core/errors.CloseInto` and returned to callers.
- **Single-attempt execution**: No uncoordinated SDK-level retry loops or uncontrolled exponential backoffs; callers and pipeline policies control retries.
- **Governance alignment**: Adheres to root invariants: zero `init()` side effects (AGENTS.md rule 4), `core/clock` for time operations (AGENTS.md rule 6), no global logger (AGENTS.md rule 7; the clients emit no logs), and HISS-14 append-only public ABI stability.

Issue #1182 evaluates whether to replace these hand-written provider clients with upstream official provider SDKs: `github.com/openai/openai-go/v3`, `github.com/anthropics/anthropic-sdk-go/v2`, and `github.com/ollama/ollama/api`.

## Considered options

| Option | Licence | Measured New Dependencies | Upstream Invariant Conflicts | Evaluation |
| :--- | :--- | :--- | :--- | :--- |
| **a) Adopt all three provider SDKs** | Apache-2.0 / MIT | +8 unique root paths (+3 OpenAI, +3 Anthropic, +2 Ollama) | 319 `func init()` in OpenAI; 112 `func init()` in Anthropic; Ollama ambient auth check, `time.Now()`, global `slog.Info`, 9 server advisories | Rejects: Imports violate AGENTS.md rules 4, 6, and 7; requires 340–520 adapter LOC plus 100–180 shared guard LOC (engineering estimates), yielding no net code reduction; cannot expose SDK tool/multimodal features through current text-only contract. |
| **b) OpenAI v3 + Anthropic v2 SDKs, keep native Ollama** | Apache-2.0 | +6 unique root paths (+3 OpenAI, +3 Anthropic) | 319 `func init()` in OpenAI; 112 `func init()` in Anthropic; stdlib `time.Now()` in request timing | Rejects now: Avoids Ollama server module issues, but still violates zero `init()` rule; requires bounds/termination adapter shims. Strongest selective alternative if policy exceptions and structured tool surfaces are approved later. |
| **c) Keep hand-written clients** | EUPL-1.2 | +0 dependencies | None | **Chosen**: Retains full control over byte limits, strict stream termination, zero `func init()` side effects, and clean error handling across 642 LOC in-tree. |

### Technical analysis of candidate SDKs

#### OpenAI Go SDK (`github.com/openai/openai-go/v3` v3.90.0)

- **Initialization side effects**: Contains 319 `func init()` declarations across non-test packages, performing import-time registration into global maps (150 union registrations, 169 field-validator registrations; see global map at [`internal/apijson/registry.go:15`](https://github.com/openai/openai-go/blob/v3.90.0/internal/apijson/registry.go#L15) and example registration at [`chatcompletion.go:837`](https://github.com/openai/openai-go/blob/v3.90.0/chatcompletion.go#L837)). This violates AGENTS.md rule 4 (zero `init()` side effects).
- **Default buffering and bounds**: Decodes response bodies via unbounded `io.ReadAll` ([`internal/requestconfig/requestconfig.go:749, :791`](https://github.com/openai/openai-go/blob/v3.90.0/internal/requestconfig/requestconfig.go#L749)); SSE line scanner allows up to 32 MiB lines ([`packages/ssestream/ssestream.go:44`](https://github.com/openai/openai-go/blob/v3.90.0/packages/ssestream/ssestream.go#L44)) with unbounded multi-line event accumulation. Does not support the framework's strict 4 MiB response / 64 KiB error / 1 MiB frame boundaries without custom body-limiting wrappers.
- **Stream termination**: SSE decoder internally consumes `[DONE]` sentinels ([`packages/ssestream/ssestream.go:253`](https://github.com/openai/openai-go/blob/v3.90.0/packages/ssestream/ssestream.go#L253)) without exposing sentinel presence to callers; clean EOF without `[DONE]` returns a nil error on finish ([`:247`](https://github.com/openai/openai-go/blob/v3.90.0/packages/ssestream/ssestream.go#L247)), violating the required `ErrStreamTruncated` contract.
- **Retries and time**: Retries twice by default with exponential backoff and jitter ([`internal/requestconfig/requestconfig.go:286`](https://github.com/openai/openai-go/blob/v3.90.0/internal/requestconfig/requestconfig.go#L286)), calling stdlib `time.Now()` ([`:667`](https://github.com/openai/openai-go/blob/v3.90.0/internal/requestconfig/requestconfig.go#L667)) instead of `core/clock`.
- **Dependency impact**: Adds 3 unique module paths (`openai-go/v3`, `Azure/azure-sdk-for-go/sdk/azcore`, `Azure/azure-sdk-for-go/sdk/internal`).
- **Adapter estimate**: Estimated 120–180 LOC for adapter implementation (engineering estimate), excluding shared bounds and stream termination observers.

#### Anthropic Go SDK (`github.com/anthropics/anthropic-sdk-go/v2` v2.4.0)

- **Initialization side effects**: Contains 112 `func init()` declarations in non-test packages registering union types into global maps (see [`internal/apijson/registry.go:15`](https://github.com/anthropics/anthropic-sdk-go/blob/v2.4.0/internal/apijson/registry.go#L15) and example registration at [`message.go:2735`](https://github.com/anthropics/anthropic-sdk-go/blob/v2.4.0/message.go#L2735)), violating AGENTS.md rule 4.
- **Global state and logging**: Client setup warns on conflicting credentials using stdlib `log.Print` ([`internal/auth/logging.go:13, :36`](https://github.com/anthropics/anthropic-sdk-go/blob/v2.4.0/internal/auth/logging.go#L13)), violating AGENTS.md rule 7.
- **Buffering and stream termination**: Unbounded body buffering via `readBody` / `io.ReadAll` ([`internal/requestconfig/requestconfig.go:457, :465`](https://github.com/anthropics/anthropic-sdk-go/blob/v2.4.0/internal/requestconfig/requestconfig.go#L457)); SSE stream scanner maximum line is 32 MiB ([`packages/ssestream/ssestream.go:40`](https://github.com/anthropics/anthropic-sdk-go/blob/v2.4.0/packages/ssestream/ssestream.go#L40)). Incomplete stream EOF leaves application error nil ([`internal/tracing/tracing.go:496`](https://github.com/anthropics/anthropic-sdk-go/blob/v2.4.0/internal/tracing/tracing.go#L496)), omitting mandatory `message_stop` verification.
- **Retries and time**: Retries twice by default ([`internal/requestconfig/requestconfig.go:175`](https://github.com/anthropics/anthropic-sdk-go/blob/v2.4.0/internal/requestconfig/requestconfig.go#L175)) and calculates attempt timing with stdlib `time.Now()` ([`:582`](https://github.com/anthropics/anthropic-sdk-go/blob/v2.4.0/internal/requestconfig/requestconfig.go#L582)), violating AGENTS.md rule 6.
- **Dependency impact**: Adds 3 unique module paths (`anthropic-sdk-go/v2`, `dnaeon/go-vcr`, `standard-webhooks/libraries`).
- **Adapter estimate**: Estimated 110–170 LOC for adapter implementation (engineering estimate), excluding shared HTTP guards.

#### Ollama Module (`github.com/ollama/ollama/api` v0.40.13)

- **Ambient authentication and logging**: On every HTTP request, the client checks `envconfig.UseAuth()` / `ollama.com`, calls `time.Now()`, and signs an auth challenge ([`api/client.go:119-120, :184-186`](https://github.com/ollama/ollama/blob/v0.40.13/api/client.go#L119-L120)). Signing attempts load `~/.ollama/id_ed25519` and log failures to global `slog.Info` ([`auth/auth.go:62`](https://github.com/ollama/ollama/blob/v0.40.13/auth/auth.go#L62)), violating AGENTS.md rules 6 and 7.
- **Bounds**: Non-streaming responses buffer via unbounded `io.ReadAll` ([`api/client.go:151`](https://github.com/ollama/ollama/blob/v0.40.13/api/client.go#L151)). Streaming reads use a fixed 8,000,000-byte scanner buffer ([`api/client.go:168`](https://github.com/ollama/ollama/blob/v0.40.13/api/client.go#L168); decimal `format.MegaByte = 1000*KiloByte`, [`format/bytes.go:12`](https://github.com/ollama/ollama/blob/v0.40.13/format/bytes.go#L12)), conflicting with the 1 MiB frame boundary and omitting exact-boundary sentinel errors.
- **Security advisories**: The module currently holds 9 open module-level OSV advisories (GO-2025-3557, GO-2025-3558, GO-2025-3559, GO-2025-3582, GO-2025-3689, GO-2025-3695, GO-2025-3824, GO-2025-4251, GO-2026-5750). While GO-2026-5750 resides in `x/imagegen/transfer` (outside `/api`) and reachability from `/api` for the remaining 8 is unverified due to lack of symbol metadata in returned OSV records, importing the module attaches server-module vulnerability posture to the root repository.
- **Dependency impact**: Adds 2 unique module paths (`ollama`, `wk8/go-ordered-map/v2`) and upgrades 3 existing modules (`locales`, `universal-translator`, `cpuid/v2`).
- **Adapter estimate**: Estimated 110–170 LOC for adapter implementation (engineering estimate), excluding shared HTTP guards.

### Adapter estimation and contract impedance

Delegating to provider SDKs would not eliminate local wrapper code:
- Engineering estimates indicate adapter shims would require:
  - OpenAI adapter: 120–180 LOC (engineering estimate).
  - Anthropic adapter: 110–170 LOC (engineering estimate).
  - Ollama adapter: 110–170 LOC (engineering estimate).
  - Shared bounds, line observers, terminator validators, and close-error wrappers: 100–180 LOC (engineering estimate).
- Total estimated adapter code: 440–700 LOC (engineering estimate), compared to the current 642 LOC of hand-written wire logic plus 29 lines of shared scanner. There is no net code reduction.
- The framework's public contract is strictly text-only `Message`, `Response`, and `Chunk`. Rich SDK features such as tool-calling schemas, structured output blocks, and detailed finish reasons cannot cross the existing interface without expanding the public API (HISS-14).

## Decision

Decline third-party provider SDKs for now; retain the hand-written LLM provider clients in `ai/llm`, `ai/llm/anthropic`, and `ai/llm/ollama` (Option c).

## Consequences

- **Positive**: Zero third-party dependency additions across the root module graph (avoids adding 8 new module paths and unexpected transitive upgrades).
- **Positive**: Complies fully with AGENTS.md rule 4 by avoiding 431 generated `func init()` registrations across OpenAI (319) and Anthropic (112) packages.
- **Positive**: Complies with AGENTS.md rules 6 and 7 by avoiding ambient filesystem auth reads, stdlib `time.Now()` calls, and global logger invocations present in the Ollama and Anthropic sources.
- **Positive**: Avoids inheriting 9 module-level OSV advisories associated with the Ollama server module.
- **Positive**: Preserves rigorous HTTP bounds enforcement (120s timeout, 4 MiB response, 64 KiB error body, 1 MiB stream frame) and verified stream truncation detection (`ErrStreamTruncated` on premature EOF), backed by regression tests in [`ai/llm/http_boundary_test.go`](https://github.com/golusoris/golusoris/blob/main/ai/llm/http_boundary_test.go).
- **Positive**: Needs no public API change (HISS-14) and no adapter layer.
- **Negative**: Maintainers own ongoing protocol adjustments and JSON schema parsing as provider wire APIs evolve.
- **Negative**: Provider-specific advanced capabilities (tool-calling schemas, structured blocks, multimodal inputs) are not natively marshaled by the text-only `Client` contract.
- **Revisit triggers**:
  1. A public structured/tool-calling or multimodal surface is added to `ai/llm`.
  2. Measured maintenance cost of provider API drift justifies delegation.
  Should either trigger occur, evaluate Option b (OpenAI v3 + Anthropic v2 SDKs, keeping native Ollama).

## References

- Issue #1182: LLM Provider SDK Evaluation.
- [`ai/llm/llm.go`](https://github.com/golusoris/golusoris/blob/main/ai/llm/llm.go) (OpenAI client, core types, stream runner).
- [`ai/llm/anthropic/anthropic.go`](https://github.com/golusoris/golusoris/blob/main/ai/llm/anthropic/anthropic.go) (Anthropic client).
- [`ai/llm/ollama/ollama.go`](https://github.com/golusoris/golusoris/blob/main/ai/llm/ollama/ollama.go) (Ollama client).
- [`ai/llm/http_boundary_test.go`](https://github.com/golusoris/golusoris/blob/main/ai/llm/http_boundary_test.go) (HTTP bounds and stream truncation tests).
- [`ai/llm/runstream_test.go`](https://github.com/golusoris/golusoris/blob/main/ai/llm/runstream_test.go) (Stream cancellation tests).
- OpenAI Go SDK repository: [`openai/openai-go`](https://github.com/openai/openai-go/tree/v3.90.0) (v3.90.0).
- Anthropic Go SDK repository: [`anthropics/anthropic-sdk-go`](https://github.com/anthropics/anthropic-sdk-go/tree/v2.4.0) (v2.4.0).
- Ollama repository: [`ollama/ollama`](https://github.com/ollama/ollama/tree/v0.40.13) (v0.40.13).
