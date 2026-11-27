#include "wifi_mgr.hpp"
#include "wifi_mgr_fake.hpp"

/* Component tier: offline unless a test says otherwise. The FSM tests set the
 * network capability through their fixtures instead. */
static volatile bool s_connected = false;

bool wifi_mgr_is_connected(void) { return s_connected; }
void wifi_mgr_fake_set_connected(bool connected) { s_connected = connected; }
