- **BREAKING**: `k8s/metrics/prom` `Mount` and `MountFor` now return `error` instead of silently recovering from Prometheus registration panics. "Already registered" is still tolerated; any other registration failure is surfaced. HISS-07 burn-down (group `runtime`).

  ```go
  // before
  prom.Mount(r, checks)
  prom.MountFor(mux, promReg, checks)

  // after
  if err := prom.Mount(r, checks); err != nil { return err }
  if err := prom.MountFor(mux, promReg, checks); err != nil { return err }
  ```
