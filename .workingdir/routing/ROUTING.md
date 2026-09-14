<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Routing ledger

Machine-readable backlog graph (`backlog-graph.yaml`) scored by `route.py`: cost against how many nodes a step unblocks, cheapest-and-most-unblocking first. Update a node's `status`/`blocked_by` on every change and re-run `python3 .workingdir/routing/route.py`; the order below is the output at the time of the last update.

```
updated 2026-09-15T02:30:00+02:00

go id    score  status       route        title
   N28    0.00  waiting-user user         8 remaining Renovate PRs are majors/groups (goldmark v2, actions major, go 1.27.1 direct
   N29    0.00  waiting-user user         praetor#29: baseline fingerprints keyed by line number report moved-but-unchanged functi
```
