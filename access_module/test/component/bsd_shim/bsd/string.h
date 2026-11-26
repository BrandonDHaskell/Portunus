/* Minimal libbsd shim for systems where glibc already provides strlcpy/strlcat.
 * libbsd-dev is only needed for its headers; the symbols are in glibc on
 * recent Debian/Ubuntu systems (exposed via _GNU_SOURCE, which IDF always sets). */
#pragma once
#ifndef _GNU_SOURCE
#define _GNU_SOURCE
#endif
#include_next <string.h>
