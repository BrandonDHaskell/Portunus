/**
 * @file portunus_types.h
 * @brief Base types and common definitions for the Portunus system.
 *
 * This header is dependency-free (no ESP-IDF includes beyond stdint/stdbool)
 * so it can be safely included from any layer without introducing cycles.
 */

#pragma once

#include <stdint.h>
#include <stdbool.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

/**
 * @brief Standard result type used across all Portunus components.
 *
 * Mirrors ESP-IDF's esp_err_t convention but scoped to Portunus-specific
 * error codes defined in error_codes.h.
 */
typedef int32_t portunus_err_t;

/** @brief Success — no error. */
#define PORTUNUS_OK          ((portunus_err_t)0)

/** @brief Generic failure. Prefer a specific error code where possible. */
#define PORTUNUS_FAIL        ((portunus_err_t)-1)

/** @brief Maximum length for a device name / identifier string. */
#define PORTUNUS_MAX_NAME_LEN  32

/** @brief Firmware version string for the MVP build. */
#define PORTUNUS_FW_VERSION    "0.1.0-mvp"

#ifdef __cplusplus
}
#endif