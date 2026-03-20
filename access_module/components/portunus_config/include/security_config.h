/**
 * @file security_config.h
 * @brief Security configuration constants for the Portunus access module.
 *
 * Two complementary mechanisms protect server communication:
 *
 *   1. TLS (HTTPS) — channel-level encryption via mbedTLS / ESP-IDF cert bundle.
 *      Prevents eavesdropping and MITM attacks on the network.
 *      Enabled by CONFIG_PORTUNUS_USE_TLS (Kconfig).
 *
 *   2. HMAC-SHA256 request signing — application-level message authentication.
 *      Every outbound POST includes an X-Portunus-Sig header containing
 *      HMAC-SHA256(pre_shared_key, request_body_bytes).
 *      The server rejects any request whose signature does not match.
 *      Enabled by CONFIG_PORTUNUS_HMAC_ENABLED (Kconfig).
 *
 * Key management notes:
 *   • The HMAC secret (CONFIG_PORTUNUS_HMAC_SECRET) is stored in flash as
 *     part of the built firmware. Treat firmware binaries as sensitive.
 *   • Rotate the HMAC secret by reflashing devices + updating the server
 *     environment variable.
 *   • For production, store the secret in an NVS encrypted partition rather
 *     than Kconfig (planned Phase 3 enhancement).
 */

#pragma once

#include "sdkconfig.h"

#ifdef __cplusplus
extern "C" {
#endif

/* ── TLS (HTTPS) ─────────────────────────────────────────────────────────── */

/** 1 when TLS is enabled; 0 otherwise. */
#ifdef CONFIG_PORTUNUS_USE_TLS
  #define PORTUNUS_USE_TLS  1
#else
  #define PORTUNUS_USE_TLS  0
#endif

/** HTTPS port (only meaningful when PORTUNUS_USE_TLS == 1). */
#if PORTUNUS_USE_TLS
  #define PORTUNUS_TLS_SERVER_PORT  CONFIG_PORTUNUS_TLS_SERVER_PORT
#endif

/**
 * @brief Skip TLS cert verification (INSECURE – dev only).
 *
 * NEVER set to 1 in production: it defeats the entire point of TLS.
 */
#ifdef CONFIG_PORTUNUS_TLS_SKIP_VERIFY
  #define PORTUNUS_TLS_SKIP_VERIFY  1
#else
  #define PORTUNUS_TLS_SKIP_VERIFY  0
#endif

/* ── HMAC-SHA256 request signing ─────────────────────────────────────────── */

/** 1 when HMAC signing is enabled; 0 otherwise. */
#ifdef CONFIG_PORTUNUS_HMAC_ENABLED
  #define PORTUNUS_HMAC_ENABLED  1
#else
  #define PORTUNUS_HMAC_ENABLED  0
#endif

/** Pre-shared HMAC key – must match PORTUNUS_HMAC_SECRET on the server. */
#if PORTUNUS_HMAC_ENABLED
  #define PORTUNUS_HMAC_SECRET  CONFIG_PORTUNUS_HMAC_SECRET
#endif

/** HTTP header name for the HMAC signature. */
#define PORTUNUS_HMAC_HEADER_NAME  "X-Portunus-Sig"

/** Hex-encoded HMAC-SHA256 is always 64 chars + NUL. */
#define PORTUNUS_HMAC_HEX_LEN  65

#ifdef __cplusplus
}
#endif