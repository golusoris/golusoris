- **HISS-20 enforcement coverage catalogue**: `.config/hiss/coverage.yaml` declares, for each
  of the 20 HISS invariants and each language this repository actually contains (Go, Python,
  C), what enforces it here, what does not, and the measurement behind the claim. 73 fixtures
  under `.config/hiss/testdata/` back the claims, and `praetorctl hiss coverage --verify`
  replays them in both directions — a claim of detection must fire on its `positive/`
  fixtures, a claim of absence must stay silent on its `gap/` ones. Wired into
  `make verify-all` (target `hiss-coverage`) and the lefthook pre-commit governance block next
  to `hiss-audit`. Four invariants are now recorded as having no enforcement here at all
  (HISS-03, HISS-06, HISS-08/Go, HISS-18) and two as manual (HISS-14, HISS-17).
