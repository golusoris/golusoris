<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Routing ledger

Machine-readable backlog graph (`backlog-graph.yaml`) scored by `route.py`: cost against how many nodes a step unblocks, cheapest-and-most-unblocking first. Update a node's `status`/`blocked_by` on every change and re-run `python3 .workingdir/routing/route.py`; the order below is the output at the time of the last update.

```
updated 2026-09-14T23:25:00+02:00

go id    score  status       route        title
>> N1    10.00  running      deterministic#447 CI green on 6255296 (rerun attempt 2 in flight)
>> N23    0.50  ready        sonnet       5 pre-existing HISS-19 duplicate blocks on #447 baseline (copyStringMap gemma/litert; se
>> N12    0.00  ready        fable        Diagnose runner-fleet degradation 19:18-19:54 (listeners restarted 19:15; anonymous Dock
>> N11    0.00  ready        deterministicrenovate.json: lockFileMaintenance for tools/spectral transitive npm CVEs
>> N20    0.00  ready        user         ci.yml has no push:main trigger — main never re-verified after merges
>> N17    0.00  ready        user         7 high Scorecard findings (checked-in binaries: bpf .o, pulumi/multiregion; code-review 
   N2     9.00  blocked      deterministicSquash-merge #447 (admin merge past merge-freeze-447)
   N3     3.00  blocked      deterministicRenovate CI guard PR (unguard.py) on new main, CI, merge
   N7     2.00  blocked      deterministicLift merge-freeze-447 from main protection
   N4     2.00  blocked      deterministicRebase 12 hiss branches --onto new main (post-merge.sh), force-push
   N5     1.00  blocked      deterministicHousekeeping PR on new main: AGENTS.md trim (N21) + regenerated .standards.lock/.config/
   N8     0.00  blocked      deterministic31 armed Renovate PRs drain against real CI
   N9     0.00  blocked      deterministic12 hiss PRs (now targeting main) get CI, merge when green
   N6     0.00  blocked      deterministicRemove 17 session worktrees + wip/* branches
   N15    0.00  waiting-user user         7 repos hold uncommitted .needs.yaml from the buggy scanner (vmafx, 20-watts understated
   N18    0.00  waiting-user user         dogfood/standards-adoption disposition (harness files for #431-#434)
   N21    0.00  blocked      sonnet       AGENTS.md compiles WHOLE-FILE to 475 > 300 (praetor render.go): collapse the 252-line ha
   N22    0.00  blocked      deterministic.standards.lock has no sha256 digest → standardsctl audit fails locally (also with praet
   N24    0.00  ready        deterministicWire HISS-17/18/19 tooling into golusoris: Makefile/lefthook targets for `dedupe scan` (
```
