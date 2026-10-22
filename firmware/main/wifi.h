#pragma once

#include <stdbool.h>

// wifi_start connects to the configured network and keeps reconnecting.
// on_connected is called from the event task each time an IP address is obtained.
void wifi_start(void (*on_connected)(void));

bool wifi_connected(void);
