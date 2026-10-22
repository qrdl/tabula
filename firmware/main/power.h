#pragma once

#include <esp_err.h>

// power_init configures the AXP2101 power chip the way Waveshare's firmware does.
esp_err_t power_init(void);
