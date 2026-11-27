# server_comm variants of the component tier, layered on sdkconfig.defaults
# (built with -DPORTUNUS_TEST_SERVER_COMM=ON):
#   sc        access point, HMAC on (production defaults)
#   sc-nohmac + sdkconfig.defaults.sc_nohmac
#   sc-peu    + sdkconfig.defaults.peu
# The test project has no certs/ca_cert.pem to embed, so use the bundle path;
# the fake grpc_client ignores TLS settings.
# CONFIG_PORTUNUS_TLS_USE_CUSTOM_CA is not set
