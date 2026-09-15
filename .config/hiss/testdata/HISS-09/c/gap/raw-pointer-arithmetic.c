// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

/* Reads past the caller-declared bound with no proof of any kind. The native
   scanner implements no reference-safety rule at all. */
int read_at(const int *base, int offset)
{
	return *(base + offset);
}
