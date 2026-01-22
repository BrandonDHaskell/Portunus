#include "sdkconfig.h"
#include "portunus_config.h"

extern "C" const char* portunus_fw_version() {
    return CONFIG_PORTUNUS_FW_VERSION;
}

extern "C" const char* portunus_module_id() {
    return CONFIG_PORTUNUS_MODULE_ID;
}

extern "C" const char* portunus_server_base_url() {
    return CONFIG_PORTUNUS_SERVER_BASE_URL;
}
