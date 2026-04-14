# ai/llm/anthropic

Anthropic Messages API client implementing `llm.Client`.

## Surface

- `anthropic.New(Config)` → `*Client`.
- `Config{APIKey, Model, MaxTokens, Endpoint, Version, Timeout, HTTPClient}`.
- `DefaultEndpoint` / `DefaultVersion` constants.

## Notes

- Raw HTTP — no SDK. POSTs JSON to `/v1/messages` with
  `x-api-key` + `anthropic-version` headers.
- `Chat` returns the concatenated text of all `type:"text"` content
  blocks. Tool-use blocks are ignored for now; add surfacing when an
  app needs it.
- `Stream` consumes SSE `content_block_delta` events with
  `delta.type == "text_delta"`. Ignores `message_start`,
  `message_delta`, `message_stop`, `ping`, and tool-use events.
- Anthropic **requires** `max_tokens` on every request — an unset
  `Config.MaxTokens` defaults to 1024. Override per-request with
  `llm.WithMaxTokens(n)`.
- `Embed` returns an error — Anthropic has no public embeddings
  endpoint. Use an OpenAI-compatible embeddings provider via
  `ai/llm.OpenAIClient` for vectors, then store through
  `ai/vector`.
- Options from `ai/llm` (WithModel/WithMaxTokens/WithTemperature/WithSystem)
  resolve through `llm.Resolve` so this backend honours the same
  user-facing option API as `OpenAIClient`.
