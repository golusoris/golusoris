<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# redis/rueidis — v1.0.78 snapshot

Pinned: **v1.0.78**
Source: [tagged source](https://github.com/redis/rueidis/tree/v1.0.78)

## Client construction

```go
import "github.com/redis/rueidis"

client, err := rueidis.NewClient(rueidis.ClientOption{
    InitAddress: []string{"localhost:6379"},
    Password:    "",
    SelectDB:    0,
})
defer client.Close()
```

## Commands

```go
// SET / GET
err := client.Do(ctx,
    client.B().Set().Key("k").Value("v").Ex(60*time.Second).Build(),
).Error()
val, err := client.Do(ctx, client.B().Get().Key("k").Build()).ToString()

// Pipeline
cmds := []rueidis.Completed{
    client.B().Set().Key("a").Value("1").Build(),
    client.B().Set().Key("b").Value("2").Build(),
}
results := client.DoMulti(ctx, cmds...)

// Pub/Sub
err = client.Receive(
    ctx,
    client.B().Subscribe().Channel("chan").Build(),
    func(msg rueidis.PubSubMessage) { handleMessage(msg.Message) },
)
```

## Distributed lock (rueidislock)

```go
import "github.com/redis/rueidis/rueidislock"

locker, err := rueidislock.NewLocker(rueidislock.LockerOption{
    ClientOption: rueidis.ClientOption{InitAddress: []string{":6379"}},
})
ctx, cancel, err := locker.TryWithContext(ctx, "resource-name")
if err != nil {
    return fmt.Errorf("acquire Redis lock: %w", err)
}
defer cancel()
```

All Redis calls use the caller's bounded `ctx`; do not replace it with
`context.Background()`.

## golusoris usage

- `cache/redis/` — `rueidis.Client` + `rueidislock.Locker` provided via fx.

## Links

- [Tagged source](https://github.com/redis/rueidis/tree/v1.0.78)
- [Package documentation](https://pkg.go.dev/github.com/redis/rueidis@v1.0.78)
