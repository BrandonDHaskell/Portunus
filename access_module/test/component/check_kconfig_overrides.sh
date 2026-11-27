#!/bin/sh
# COMP-05: the component tier runs on production Kconfig defaults (the test
# Kconfig sources main/Kconfig.projbuild), so the only way for test and
# production configuration to drift is an override in a sdkconfig.defaults*
# file. Fail on any CONFIG_PORTUNUS_* override that is not listed here with
# its reason. Usage: check_kconfig_overrides.sh [component test dir]
set -eu

dir=${1:-$(dirname "$0")}

# file:symbol  reason
allowed='
sdkconfig.defaults.peu:CONFIG_PORTUNUS_MODULE_TYPE_PROVISIONING_CONSOLE  selects the PEU variant
sdkconfig.defaults.hb:CONFIG_PORTUNUS_TEST_HEARTBEAT                     selects the heartbeat test app
sdkconfig.defaults.hb:CONFIG_PORTUNUS_HEARTBEAT_INTERVAL_MS              tests wait multiples of it
sdkconfig.defaults.sc:CONFIG_PORTUNUS_TLS_USE_CUSTOM_CA                  no certs/ca_cert.pem in the test project
sdkconfig.defaults.sc_nohmac:CONFIG_PORTUNUS_ENV_DEV                     HMAC can be off only in dev builds
sdkconfig.defaults.sc_nohmac:CONFIG_PORTUNUS_HMAC_ENABLED                SC-12 pins the no-HMAC behaviour
'

status=0
for f in "$dir"/sdkconfig.defaults*; do
    name=$(basename "$f")
    # Both "CONFIG_X=..." and "# CONFIG_X is not set" assign a value.
    syms=$(sed -nE 's/^(# )?(CONFIG_PORTUNUS_[A-Z0-9_]+)( is not set|=.*)$/\2/p' "$f")
    for sym in $syms; do
        if ! printf '%s\n' "$allowed" | grep -q "^${name}:${sym}[[:space:]]"; then
            echo "COMP-05: ${name} overrides ${sym}, which is not in the allowlist in $0" >&2
            status=1
        fi
    done
done

if [ "$status" -eq 0 ]; then
    echo "COMP-05: test Kconfig overrides are all allowlisted"
fi
exit "$status"
