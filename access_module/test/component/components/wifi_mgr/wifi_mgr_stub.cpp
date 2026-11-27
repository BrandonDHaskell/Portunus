#include "wifi_mgr.hpp"

/* Component tier: never connected. Tests that need the network set the FSM's
 * capability through its fixture instead. */
bool wifi_mgr_is_connected(void) { return false; }
