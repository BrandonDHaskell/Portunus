/* Minimal libbsd shim: glibc's <sys/queue.h> plus the BSD iterator it lacks.
 * ESP-IDF's esp_partition (pulled in by the NVS variant) prefers
 * <bsd/sys/queue.h> and uses SLIST_FOREACH_SAFE, which glibc omits.
 *
 * include_next, not include: IDF's linux/include/sys/queue.h itself includes
 * <bsd/sys/queue.h> when it exists, so a plain include would come back to
 * IDF's header and stop at its include guard. bsd_shim is last on the
 * include path, so include_next reaches glibc's header. */
#pragma once
#include_next <sys/queue.h>

#ifndef SLIST_FOREACH_SAFE
#define SLIST_FOREACH_SAFE(var, head, field, tvar)                 \
    for ((var) = SLIST_FIRST((head));                              \
         (var) && ((tvar) = SLIST_NEXT((var), field), 1);          \
         (var) = (tvar))
#endif
