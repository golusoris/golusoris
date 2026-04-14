# redis/rueidis — v1.0.54 snapshot

Pinned: **v1.0.54**
Source: https://pkg.go.dev/github.com/redis/rueidis@v1.0.54

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
ctx := context.Background()

// SET / GET
err := client.Do(ctx, client.B().Set().Key("k").Value("v").Ex(60).Build()).Error()
val, err := client.Do(ctx, client.B().Get().Key("k").Build()).ToString()

// Pipeline
cmds := []rueidis.Completed{
    client.B().Set().Key("a").Value("1").Build(),
    client.B().Set().Key("b").Value("2").Build(),
}
results := client.DoMulti(ctx, cmds...)

// Pub/Sub
err = client.Receive(ctx, client.B().Subscribe().Channel("chan").Build(), func(msg rueidis.PubSubMessage) {
    // handle msg.Message
})
```

## Distributed lock (rueidislock)

```go
import "github.com/redis/rueidis/rueidislock"

locker, err := rueidislock.NewLocker(rueidislock.LockerOption{
    ClientOption: rueidis.ClientOption{InitAddress: []string{":6379"}},
})
ctx, cancel, err := locker.TryWithContext(ctx, "resource-name")
if err != nil { /* lock not acquired */ }
defer cancel()
```

## golusoris usage

- `cache/redis/` — `rueidis.Client` + `rueidislock.Locker` provided via fx.

## Links

- Changelog: https://github.com/redis/rueidis/blob/main/CHANGELOG.md
- Examples: https://github.com/redis/rueidis/tree/main/examples
