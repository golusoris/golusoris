# Agent guide — fs/watch/

Debounced recursive directory watcher. Collapses bursts of filesystem events
(e.g. editor multi-write on save) into a single `Event{Paths}` per debounce
window.

## API

```go
w, err := watch.New(watch.Options{Debounce: 200 * time.Millisecond})
defer w.Close()
w.Add("/var/config")

for ev := range w.Events() {
    log.Println("changed:", ev.Paths)
}
```

## Options

| Field | Default | Notes |
|---|---|---|
| `Debounce` | 100ms | Wait after last event before emitting |
| `BufferSize` | 16 | Events channel capacity; slow consumers drop events |

## Don't

- Don't call `Add` for every file in a large tree — add the parent directory.
- Don't block inside the `Events()` loop — the channel has finite capacity.
- Don't use for security-critical file-integrity monitoring — use a dedicated
  FIM tool (auditd, AIDE) instead.
