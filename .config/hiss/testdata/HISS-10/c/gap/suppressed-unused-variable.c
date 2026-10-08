// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

/* An unexplained suppression accepted by both Clang authorities. */
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wunused-variable"
int describe(int n)
{
	int scale = 3; // NOLINT
	return n + 1;
}
#pragma clang diagnostic pop
