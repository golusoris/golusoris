<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — ai/llm/

Provider-agnostic LLM interface + OpenAI-compatible HTTP client.

## Client interface

```go
type Client interface {
    Chat(ctx, messages, ...Option) (Response, error)
    Stream(ctx, messages, ...Option) <-chan Chunk
    Embed(ctx, text) ([]float32, error)
}
```

## Options

| Option | Effect |
| --- | --- |
| `WithModel(name)` | Override default model for this call |
| `WithMaxTokens(n)` | Cap output token budget |
| `WithTemperature(t)` | Sampling temperature (0=deterministic, 1=creative) |
| `WithSystem(prompt)` | Prepend a system message |

## OpenAIClient

Works with any OpenAI-compatible endpoint:

```go
client, err := llm.NewOpenAIClient(llm.Config{
    BaseURL: "https://api.openai.com/v1",  // or Ollama: "http://localhost:11434/v1"
    APIKey:  os.Getenv("OPENAI_API_KEY"),
    Model:   "gpt-4o-mini",
    EmbedModel: "text-embedding-3-small",
})
if err != nil {
    return err
}
```

Constructor: `NewOpenAIClient(Config) (*OpenAIClient, error)`.

HTTP bounds:

| Config field | Zero-value default |
| --- | ---: |
| `Timeout` | 120s |
| `MaxResponseBytes` | 4 MiB |
| `MaxErrorBytes` | 64 KiB |
| `MaxStreamFrameBytes` | 1 MiB |

Negative or sentinel-unbounded values: constructor error. `HTTPClient`: cloned;
caller state preserved; configured timeout applied when source timeout non-positive.

## Stream usage

```go
for chunk := range client.Stream(ctx, messages) {
    if chunk.Err != nil { /* handle */ }
    fmt.Print(chunk.Content)
}
```

Consumer exit before channel close: cancel `ctx`. Producer sends observe
cancellation. Scanner/read failures arrive as terminal `Chunk{Err: err}`.
Clean EOF before the provider terminator, malformed data frames, and provider
error events also arrive as terminal errors; partial content is never success.

## Provider sub-packages

- `ai/llm/anthropic/` — Anthropic Messages API.
- `ai/llm/ollama/` — Ollama native API.

## Guardrails

- Message content: sensitive; omit from logs.
- API keys: load through `golusoris/secrets` or environment.
- Streams: drain channel or cancel context.
