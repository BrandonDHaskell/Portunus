/**
 * @file access_point_gpio.cpp
 * @brief IAccessPoint implementation using GPIO door strike and reed switch.
 *
 * Delegates to the internal door_strike and reed_switch HAL functions.
 * Each capability is feature-gated via Kconfig so the project can be
 * built for bench testing (e.g., no strike hardware connected).
 *
 * is_open() inverts reed_switch_is_closed() because the interface uses
 * positive logic (open = true) while the reed switch uses physical
 * logic (closed = true).
 */

#include "access_point_gpio.h"
#include "error_codes.h"

#include "esp_log.h"

#ifdef CONFIG_PORTUNUS_ENABLE_DOOR_STRIKE
#include "door_strike.h"    /* internal HAL */
#endif

#ifdef CONFIG_PORTUNUS_ENABLE_REED_SWITCH
#include "reed_switch.h"    /* internal HAL */
#endif

static const char *TAG = "access_point_gpio";

portunus_err_t AccessPointGpio::init()
{
    ESP_LOGI(TAG, "Initialising GPIO access point");

    bool any_capability = false;

#ifdef CONFIG_PORTUNUS_ENABLE_DOOR_STRIKE
    {
        portunus_err_t err = door_strike_init();
        if (err != PORTUNUS_OK) {
            ESP_LOGE(TAG, "Door strike init failed: 0x%04x", err);
            return err;
        }
        any_capability = true;
    }
#endif

#ifdef CONFIG_PORTUNUS_ENABLE_REED_SWITCH
    {
        portunus_err_t err = reed_switch_init();
        if (err != PORTUNUS_OK) {
            ESP_LOGE(TAG, "Reed switch init failed: 0x%04x", err);
            return err;
        }
        any_capability = true;
    }
#endif

    if (!any_capability) {
        ESP_LOGW(TAG, "No door hardware enabled in Kconfig");
        return PORTUNUS_ERR_DEVICE_NOT_FOUND;
    }

    ESP_LOGI(TAG, "GPIO access point initialised");
    return PORTUNUS_OK;
}

portunus_err_t AccessPointGpio::unlock()
{
#ifdef CONFIG_PORTUNUS_ENABLE_DOOR_STRIKE
    return door_strike_energize();
#else
    return PORTUNUS_ERR_DEVICE_NOT_FOUND;
#endif
}

portunus_err_t AccessPointGpio::lock()
{
#ifdef CONFIG_PORTUNUS_ENABLE_DOOR_STRIKE
    return door_strike_deenergize();
#else
    return PORTUNUS_ERR_DEVICE_NOT_FOUND;
#endif
}

bool AccessPointGpio::is_open()
{
#ifdef CONFIG_PORTUNUS_ENABLE_REED_SWITCH
    /* Interface: true = open. Reed switch: true = closed. */
    return !reed_switch_is_closed();
#else
    /* No door sensor available; treat as "not open". */
    return false;
#endif
}
