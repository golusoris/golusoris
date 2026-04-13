# Agent guide — testutil/fxtest/

Thin wrapper around `go.uber.org/fx/fxtest` that integrates with `*testing.T`
lifecycle — starts the app, stops it via `t.Cleanup`, and fatals on error.

## Usage

```go
func TestMyService(t *testing.T) {
    var svc *myservice.Service
    fxtest.New(t,
        myservice.Module,
        fxtest.Populate(&svc),
    )
    // svc is started; app stops when the test function returns
    got, err := svc.DoThing(context.Background())
    require.NoError(t, err)
    assert.Equal(t, "expected", got)
}
```

## Don't

- Don't call `app.Start` / `app.Stop` manually — `New` handles both.
- Don't use this for benchmarks that need fine-grained lifecycle control;
  use `fxtest.New(b, ...)` from go.uber.org/fx/fxtest directly.
