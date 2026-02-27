/**
 * @file security_config.h
 * @brief Security configuration constants for the Portunus access module.
 *
 * Reserved for Phase 2. Planned additions:
 *   - TLS certificate pinning for server communication
 *   - HTTPS toggle (CONFIG_PORTUNUS_USE_TLS)
 *   - CA certificate bundle selection
 *   - Credential encryption-at-rest parameters (NVS)
 *
 * Until this header is populated, server_comm uses plain HTTP.
 */

#pragma once

#ifdef __cplusplus
extern "C" {
#endif

/* No configuration constants yet — see Phase 2 roadmap. */

#ifdef __cplusplus
}
#endif