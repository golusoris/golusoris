# jobs/workflow

Temporal workflow orchestration fx module.

## Surface

- `workflow.Module` — provides `client.Client` + `worker.Worker` into fx.
- `workflow.Config{Host, Namespace, TaskQueue, TLS, APIKey, Identity}` — loaded from `workflow.*` config keys.
- `workflow.Client` = `client.Client` alias. `workflow.Worker` = `worker.Worker` alias.
- `workflow.DefaultConfig()` → local-dev defaults (`localhost:7233`, namespace `"default"`).

## Notes

- **Worker vs. producer-only**: set `TaskQueue` to get a started worker; omit it for insert-only services that just call `client.ExecuteWorkflow`.
- **Temporal Cloud**: set `TLS=true` + `APIKey=<your key>`. `Host` should be `<namespace>.tmprl.cloud:7233`.
- **Register workflows + activities** in `fx.Invoke`:
  ```go
  fx.Invoke(func(w workflow.Worker) {
      w.RegisterWorkflow(MyWorkflow)
      w.RegisterActivity(MyActivities{})
  })
  ```
- **Logging**: uses `log.NewStructuredLogger` (Temporal's official slog bridge — part of the SDK).
- **Lifecycle**: client is closed on fx Stop; worker is started on fx Start + stopped on fx Stop.
- Dep: `go.temporal.io/sdk` (single SDK, no extra sub-modules needed for the core).
