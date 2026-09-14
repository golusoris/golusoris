# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2
#!/usr/bin/env python3
"""Recompute the execution order from backlog-graph.yaml.

Score = (nodes transitively unblocked) / cost weight, ready nodes only.
Cheapest-and-most-unblocking first. Re-run after every status change.
"""
import sys, yaml, pathlib

COST = {"none": 1, "cheap": 2, "frontier": 5}
here = pathlib.Path(__file__).parent
g = yaml.safe_load(open(here / "backlog-graph.yaml"))
nodes = {n["id"]: n for n in g["nodes"]}
children = {i: [] for i in nodes}
for n in nodes.values():
    for b in n.get("blocked_by", []):
        children.setdefault(b, []).append(n["id"])

def downstream(i, seen=None):
    seen = seen or set()
    for c in children.get(i, []):
        if c not in seen:
            seen.add(c); downstream(c, seen)
    return seen

def actionable(n):
    return n["status"] in ("ready", "running") and all(nodes[b]["status"] == "done" for b in n.get("blocked_by", []))

rows = []
for i, n in nodes.items():
    unb = downstream(i)
    w = COST.get(n["cost"]["t"], 3)
    rows.append((actionable(n), len(unb) / w, i, n))

rows.sort(key=lambda r: (not r[0], -r[1]))
print(f"updated {g['updated']}\n")
print(f"{'go':<3}{'id':<5}{'score':>6}  {'status':<13}{'route':<13}title")
for act, score, i, n in rows:
    if n["status"] == "done":
        continue
    print(f"{'>>' if act else '  ':<3}{i:<5}{score:>6.2f}  {n['status']:<13}{n['route']:<13}{n['title'][:88]}")
