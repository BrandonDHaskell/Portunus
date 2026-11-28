#!/usr/bin/env bash
# assert_secure_build.sh: refuse a firmware image whose compiled config is not
# deployable.
#
# Checks build/config/sdkconfig.h (what was actually compiled), not sdkconfig:
# cached CMake defaults can make the two differ.
#
# Usage: scripts/assert_secure_build.sh [--prod] path/to/build/config/sdkconfig.h
#   --prod   also require the pinned private CA (CONFIG_PORTUNUS_TLS_USE_CUSTOM_CA)
#
# Exit codes: 0 deployable, 1 insecure, 2 usage error or missing header.
set -euo pipefail

PROD=0
if [[ "${1:-}" == "--prod" ]]; then
    PROD=1
    shift
fi
if [[ $# -ne 1 ]]; then
    echo "usage: $0 [--prod] path/to/sdkconfig.h" >&2
    exit 2
fi
H="$1"
if [[ ! -f "$H" ]]; then
    echo "ERROR: $H not found (did the build run?)" >&2
    exit 2
fi

has() { grep -qE "^#define CONFIG_$1 1\$" "$H"; }

bad=()
has PORTUNUS_ENV_DEV               && bad+=("PORTUNUS_ENV_DEV on")
has PORTUNUS_TLS_SKIP_VERIFY       && bad+=("PORTUNUS_TLS_SKIP_VERIFY on")
has ESP_TLS_INSECURE               && bad+=("ESP_TLS_INSECURE on")
has ESP_TLS_SKIP_SERVER_CERT_VERIFY && bad+=("ESP_TLS_SKIP_SERVER_CERT_VERIFY on")
if [[ $PROD -eq 1 ]] || has PORTUNUS_ENABLE_WIFI; then
    has PORTUNUS_USE_TLS      || bad+=("PORTUNUS_USE_TLS off")
    has PORTUNUS_HMAC_ENABLED || bad+=("PORTUNUS_HMAC_ENABLED off")
fi
if [[ $PROD -eq 1 ]]; then
    has PORTUNUS_ENABLE_WIFI          || bad+=("PORTUNUS_ENABLE_WIFI off")
    has PORTUNUS_TLS_USE_CUSTOM_CA    || bad+=("PORTUNUS_TLS_USE_CUSTOM_CA off (CA not pinned)")
fi

if [[ ${#bad[@]} -gt 0 ]]; then
    echo "ERROR: $H is NOT a deployable image:" >&2
    printf '  %s\n' "${bad[@]}" >&2
    echo "  Prod image:      task firmware:build:prod" >&2
    echo "  Dev image:       task firmware:build:dev" >&2
    echo "  Compile check:   task firmware:build ALLOW_INSECURE=1" >&2
    exit 1
fi
echo "Secure-build check passed: $H"
