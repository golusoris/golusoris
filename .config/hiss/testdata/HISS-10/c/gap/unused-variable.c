// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

/* gcc -Wall reports [-Wunused-variable] here. No gate in this repository
   compiles a .c file at all, so nothing reports it. */
int describe(int n)
{
	int scale = 3;
	return n + 1;
}
