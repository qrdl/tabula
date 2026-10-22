#pragma once

typedef enum {
    BUTTON_BOOT,
    BUTTON_KEY,
} button_t;

// buttons_start watches the BOOT and KEY buttons and calls on_press, from its own
// task, for every short press (long presses are ignored). The green LED blinks on each press.
void buttons_start(void (*on_press)(button_t button));
