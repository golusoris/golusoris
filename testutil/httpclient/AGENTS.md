<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — testutil/httpclient/

- API: `AssertBoundedClone(t, source, clone, timeout)` -> assert clone distinct, bounded timeout, preserved transport.
- Target: constructors cloning injected `*http.Client`.
