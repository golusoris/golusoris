// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

#define MAX_POLLS 1024

int poll_bounded(volatile int *flag)
{
	for (int i = 0; i < MAX_POLLS; i++) {
		if (*flag)
			return 1;
	}
	return 0;
}
