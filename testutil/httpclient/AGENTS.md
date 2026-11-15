<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — testutil/httpclient/

- API: `AssertBoundedClone(t, source, clone, timeout)` -> assert clone distinct, bounded timeout, preserved transport.
- API: `RoundTripError(t, rt, req)` -> error of failing round trip; response returned: body closed, test failed.
- Target: constructors cloning injected `*http.Client`; transport tests without open bodies.
