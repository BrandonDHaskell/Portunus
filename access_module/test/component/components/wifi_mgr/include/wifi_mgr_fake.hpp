#pragma once
/* Test control for the wifi_mgr stub. Defaults to disconnected, the state the
 * FSM tests start from; the server_comm tests turn it on. */
void wifi_mgr_fake_set_connected(bool connected);
