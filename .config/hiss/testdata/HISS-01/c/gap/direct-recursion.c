// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

/* The native scanner is a line matcher: it decides goto and nothing else,
   so this cycle in the call graph passes unreported. */
int factorial(int n)
{
	if (n <= 1)
		return 1;
	return n * factorial(n - 1);
}
