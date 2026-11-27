# Heartbeat variant of the component tier, layered on sdkconfig.defaults:
# SDKCONFIG_DEFAULTS="sdkconfig.defaults;sdkconfig.defaults.hb"
# Runs the real heartbeat_service on the POSIX FreeRTOS scheduler. The tests
# wait multiples of the interval, so use the shortest the production Kconfig
# allows (range 1000..60000; a smaller value is silently replaced by the
# 10 s default and the variant then takes minutes).
CONFIG_PORTUNUS_TEST_HEARTBEAT=y
CONFIG_PORTUNUS_HEARTBEAT_INTERVAL_MS=1000
