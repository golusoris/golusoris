<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# ai/llm/ollama

Ollama native API client implementing `llm.Client`.

## Surface

- `ollama.New(Config)` → `(*Client, error)`.
- `Config{BaseURL, Model, EmbedModel, KeepAlive, Timeout, HTTPClient,
  MaxResponseBytes, MaxErrorBytes, MaxStreamFrameBytes}`.
- `DefaultBaseURL` = `http://localhost:11434`.

## Why this vs the OpenAI-compat endpoint?

Ollama also speaks OpenAI format at `/v1`, and `ai/llm.OpenAIClient`
with `BaseURL: "http://localhost:11434/v1"` works for basic chat. Use
this sub-package when you need:

- Ollama-specific `options` (num_predict, num_ctx, num_gpu)
- native NDJSON streaming format (simpler than SSE)
- `/api/embeddings` directly (OpenAI-compat embeddings endpoint
 sometimes lags behind on new embedding models)
- `keep_alive` control for model-warm-up management

## Notes

- Raw HTTP — no SDK. No auth headers (Ollama is typically run on localhost).
- `Chat` POSTs to `/api/chat` with `stream:false` and returns single
 message response; token counts come from `prompt_eval_count` + `eval_count`.
- `Stream` POSTs with `stream:true` and scans NDJSON, emitting `Chunk`
 values per delta until mandatory `done:true`; provider errors, malformed JSON,
 or EOF before it fail the stream.
- HTTP defaults: 120s request timeout, 4 MiB success body, 64 KiB error body,
  1 MiB stream frame. Negative or sentinel-unbounded values: constructor error.
- Injected `HTTPClient`: cloned; caller state preserved; configured timeout used
  when source timeout non-positive.
- Consumer exit before stream close: cancel context. Read/scanner failures surface
  through terminal `Chunk.Err`.
- `Embed` POSTs to `/api/embeddings` with `{model, prompt}` and returns
 `[]float32`. Falls back to `Model` when `EmbedModel` is unset.
- `llm.Option` values (WithModel/WithMaxTokens/WithTemperature/WithSystem)
 resolve through `llm.Resolve`. `MaxTokens` maps to Ollama's
 `options.num_predict`.
- `KeepAlive` accepts Ollama's duration strings (e.g. `"5m"`, `"-1"`
 to pin forever, `"0"` to unload immediately).
