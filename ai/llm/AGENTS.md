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
|---|---|
| `WithModel(name)` | Override default model for this call |
| `WithMaxTokens(n)` | Cap output token budget |
| `WithTemperature(t)` | Sampling temperature (0=deterministic, 1=creative) |
| `WithSystem(prompt)` | Prepend a system message |

## OpenAIClient

Works with any OpenAI-compatible endpoint:

```go
client := llm.NewOpenAIClient(llm.Config{
    BaseURL: "https://api.openai.com/v1",  // or Ollama: "http://localhost:11434/v1"
    APIKey:  os.Getenv("OPENAI_API_KEY"),
    Model:   "gpt-4o-mini",
    EmbedModel: "text-embedding-3-small",
})
```

## Stream usage

```go
for chunk := range client.Stream(ctx, messages) {
    if chunk.Err != nil { /* handle */ }
    fmt.Print(chunk.Content)
}
```

## Planned sub-packages

- `ai/llm/anthropic/` — Anthropic Messages API (thinking, vision, tools)
- `ai/llm/ollama/` — Ollama-specific features (model pull, list)

## Don't

- Don't log full message content — it may contain PII.
- Don't hardcode API keys — use `golusoris/secrets` or env vars.
- Don't call `Stream` without draining the channel to completion.
