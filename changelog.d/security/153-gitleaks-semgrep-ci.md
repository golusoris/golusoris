- Added a custom `.semgrep.yml` ruleset of golusoris-specific SAST invariants
  (timeout-less `http.Client`, `http.DefaultClient`, `http.ListenAndServe`,
  blocking `time.Sleep` in library code, deprecated `io/ioutil`) wired as an
  advisory CI lane and a local pre-commit hook. Promoted `gitleaks` secret
  scanning and `govulncheck` to pre-commit for shift-left supply-chain checks.
