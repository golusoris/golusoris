// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

/* Clang -Wall -Wextra -Werror must reject the unused local. */
int describe(int n)
{
	int scale = 3;
	return n + 1;
}
