// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

int accumulate(const int *xs, int n)
{
	int total = 0;
	for (int i = 0; i < n; i++)
		total += xs[i];
	return total;
}
