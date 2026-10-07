<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — testutil/load/

HTTP load-testing helpers over `tsenart/vegeta`: drive attack, then assert
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

`load.Check` is `func(*vegeta.Metrics) string` — return non-empty message to
flag violation; write custom checks inline. `Attack` aggregates results and
does not fail test — only `Assert` calls `t.Errorf`.

## Why tsenart/vegeta

- Constant-rate (open-model) HTTP attacker with percentile latency histograms
 out of box; de-facto Go load tool. `Options.Targeter` is raw
 `vegeta.Targeter`, so full vegeta API is reachable when helpers fall short.

## Notes

- Opt-in: guard `*_Load` tests with `testing.Short()` so they skip in normal CI.
- `Rate` is `vegeta.Pacer` — pass `ConstantRate(n)` or any other pacer.
