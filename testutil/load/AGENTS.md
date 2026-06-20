# Agent guide — testutil/load/

HTTP load-testing helpers over `tsenart/vegeta`: drive an attack, then assert
latency/error-rate/throughput thresholds. Stateless test utility — **no fx
wiring**. Import directly from `_test.go` files.

## API

```go
m := load.Attack(t, load.Options{
    Targeter: load.GET("http://localhost:8080/health"), // or load.POST(url, ct, body)
    Rate:     load.ConstantRate(50),                    // vegeta.Pacer; 50 rps
    Duration: 5 * time.Second,
})                                                       // returns *vegeta.Metrics; never fails t
load.Assert(t, m,
    load.MaxErrorRate(0.01),         // fraction in [0,1]
    load.MaxP99(100*time.Millisecond),
    load.MaxMean(d), load.MinThroughput(rps),
)
```

A `load.Check` is `func(*vegeta.Metrics) string` — return a non-empty message to
flag a violation; write custom checks inline. `Attack` aggregates results and
does not fail the test — only `Assert` calls `t.Errorf`.

## Why tsenart/vegeta

- Constant-rate (open-model) HTTP attacker with percentile latency histograms
  out of the box; the de-facto Go load tool. `Options.Targeter` is a raw
  `vegeta.Targeter`, so the full vegeta API is reachable when the helpers fall short.

## Notes

- Opt-in: guard `*_Load` tests with `testing.Short()` so they skip in normal CI.
- `Rate` is a `vegeta.Pacer` — pass `ConstantRate(n)` or any other pacer.
