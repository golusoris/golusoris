// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

#include <dlfcn.h>

/* Maps an unpinned, unverified shared object into the process and calls a
   symbol resolved by name. The native scanner matches three unsafe string
   primitives and nothing about dynamic loading. */
void *load_symbol(const char *path, const char *symbol)
{
	void *handle = dlopen(path, RTLD_NOW);
	if (!handle)
		return 0;
	return dlsym(handle, symbol);
}
