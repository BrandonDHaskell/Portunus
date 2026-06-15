/**
 * @file security_config.h
 * @brief Security configuration constants for the Portunus access module.
 *
 * Two mechanisms protect server communication:
 *
 *   1. TLS (HTTPS) — mandatory for ACCESS_POINT builds.  A door image cannot
 *      be compiled without TLS; the #error gate in server_comm.cpp enforces
 *      this, and the Kconfig dependency prevents menuconfig from allowing the
 *      combination.  TLS authenticates the server and encrypts the channel.
 *      Enabled by CONFIG_PORTUNUS_USE_TLS (Kconfig).
 *
 *   2. HMAC-SHA256 message authentication — belt-and-suspenders on top of TLS,
 *      not a TLS replacement.  Applied to both outbound requests (X-Portunus-Sig
 *      header) and inbound access responses (server signs the response; device
 *      verifies before publishing EVENT_ACCESS_GRANTED).  A door grant without
 *      a valid response signature is treated as a deny.
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

/**
 * @brief Pin to a custom CA certificate embedded in the firmware.
 *
 * When enabled, the ESP32 validates the server cert against the PEM at
 * access_module/certs/ca_cert.pem (embedded via EMBED_TXTFILES in
 * server_comm/CMakeLists.txt) instead of the Mozilla CA bundle.
 *
 * This is the recommended mode for LAN deployments with a private CA.
 * Generate the cert with:  ./scripts/generate_certs.sh --ip <SERVER_IP>
 */
#ifdef CONFIG_PORTUNUS_TLS_USE_CUSTOM_CA
  #define PORTUNUS_TLS_USE_CUSTOM_CA  1
#else
  #define PORTUNUS_TLS_USE_CUSTOM_CA  0
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