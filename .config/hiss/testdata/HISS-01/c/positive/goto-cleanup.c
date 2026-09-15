// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

int open_and_read(int ok)
{
	int rc = 0;
	if (!ok)
		goto out;
	rc = 1;
out:
	return rc;
}
