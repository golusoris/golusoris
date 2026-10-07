<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# ai/llm/anthropic

Anthropic Messages API client implementing `llm.Client`.

## Surface

- `anthropic.New(Config)` → `(*Client, error)`.
- `Config{APIKey, Model, MaxTokens, Endpoint, Version, Timeout, HTTPClient,
  MaxResponseBytes, MaxErrorBytes, MaxStreamFrameBytes}`.
- `DefaultEndpoint` / `DefaultVersion` constants.

## Notes

- Raw HTTP — no SDK. POSTs JSON to `/v1/messages` with
 `x-api-key` + `anthropic-version` headers.
- `Chat` returns concatenated text of all `type:"text"` content
 blocks. Tool-use blocks are ignored for now; add surfacing when  app needs it.
- `Stream` consumes SSE `content_block_delta` events with
 `delta.type == "text_delta"`. `message_stop` is mandatory completion;
 provider errors, malformed data, or EOF before it fail the stream.
 `message_start`, `message_delta`, `ping`, and tool-use events carry no text.
- HTTP defaults: 120s request timeout, 4 MiB success body, 64 KiB error body,
  1 MiB stream frame. Negative or sentinel-unbounded values: constructor error.
- Injected `HTTPClient`: cloned; caller state preserved; configured timeout used
  when source timeout non-positive.
- Consumer exit before stream close: cancel context. Read/scanner failures surface
  through terminal `Chunk.Err`.
- Anthropic **requires** `max_tokens` on every request — unset
 `Config.MaxTokens` defaults to 1024. Override per-request with
 `llm.WithMaxTokens(n)`.
- `Embed` returns error — Anthropic has no public embeddings
 endpoint. Use OpenAI-compatible embeddings provider via
 `ai/llm.OpenAIClient` for vectors, then store through
 `ai/vector`.
- Options from `ai/llm` (WithModel/WithMaxTokens/WithTemperature/WithSystem)
 resolve through `llm.Resolve` so this backend honours same
 user-facing option API as `OpenAIClient`.
