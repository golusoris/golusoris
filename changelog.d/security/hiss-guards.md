- Extended the custom `.semgrep.yml` ruleset with three enforcement rules for
  invariants the tree previously upheld only by convention: `unsafe` imports and
  `unsafe.Pointer` / `Slice` / `String` conversions now require an adjacent
  `// SAFETY:` proof (HISS-09), `plugin.Open` / `plugin.Lookup` /
  `reflect.MakeFunc` and shell-interpreter or string-assembled `os/exec` command
  names are refused (HISS-08), and a spawn inside a loop over the input — a `go`
  statement, or `Go` on an errgroup — is refused unless the bound is taken
  before it: a semaphore send or `Acquire` ahead of the `go`, or `SetLimit` on
  that same group (HISS-06). Each rule ships a positive/negative/gap fixture
  corpus under `.config/hiss/testdata/`, replayed in all three directions by
  `make hiss-fixtures` and in the CI semgrep lane.
