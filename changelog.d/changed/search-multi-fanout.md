- `search.MultiSearcher` now bounds its backend fan-out instead of spawning one
  goroutine per registered backend: at most `search.DefaultMaxFanOut` (8) run at
  once, or as many as the new `search.WithMaxFanOut` option allows. Registering
  more backends lengthens a query rather than widening its concurrency.
