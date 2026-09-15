- Extended the custom `.semgrep.yml` ruleset with three enforcement rules for
  invariants the tree previously upheld only by convention: `unsafe` imports and
  `unsafe.Pointer` / `Slice` / `String` conversions now require an adjacent
  `// SAFETY:` proof (HISS-09), `plugin.Open` / `plugin.Lookup` /
  `reflect.MakeFunc` and shell-interpreter or string-assembled `os/exec` command
  names are refused (HISS-08), and a `go` statement inside a loop over the input
  is refused unless a bound is acquired first (HISS-06). Each rule ships a
  positive/negative fixture corpus under `.config/hiss/testdata/`, replayed in
  both directions by `make hiss-fixtures` and in the CI semgrep lane.
