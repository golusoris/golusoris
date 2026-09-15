// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

int poll_forever(volatile int *flag)
{
	while (1) {
		if (*flag)
			return 1;
	}
}
