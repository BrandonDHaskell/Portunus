# Heartbeat variant of the component tier, layered on sdkconfig.defaults:
# SDKCONFIG_DEFAULTS="sdkconfig.defaults;sdkconfig.defaults.hb"
# Runs the real heartbeat_service on the POSIX FreeRTOS scheduler. The test
# Kconfig shortens HEARTBEAT_INTERVAL_MS to 100 ms when this is set (the
# symbol has no prompt, so it cannot be assigned here directly).
CONFIG_PORTUNUS_TEST_HEARTBEAT=y
