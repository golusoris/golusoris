// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

#include <string.h>

void copy_name(char *dst, size_t cap, const char *src)
{
	snprintf(dst, cap, "%s", src);
}
