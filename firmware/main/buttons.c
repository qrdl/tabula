// Buttons and LEDs. Pins and levels are from Waveshare's PhotoPainter code
// (components/port_bsp/button_bsp.c and led_bsp.h).

#include "buttons.h"

#include <driver/gpio.h>
#include <freertos/FreeRTOS.h>
#include <freertos/task.h>

#define PIN_BOOT      0  // low when pressed
#define PIN_KEY       4  // low when pressed
#define PIN_LED_GREEN 42 // lit when low
#define PIN_LED_RED   45 // lit when low

#define POLL_MS        20
#define MIN_PRESS_MS   40   // shorter is contact bounce
#define MAX_PRESS_MS   1000 // longer is a long press, ignored
#define BLINK_MS       100

static void (*press_cb)(button_t);

static void blink(void) {
    gpio_set_level(PIN_LED_GREEN, 0);
    vTaskDelay(pdMS_TO_TICKS(BLINK_MS));
    gpio_set_level(PIN_LED_GREEN, 1);
}

static void poll_task(void *arg) {
    struct {
        int pin;
        button_t id;
        int held_ms;
    } buttons[] = {{PIN_BOOT, BUTTON_BOOT, 0}, {PIN_KEY, BUTTON_KEY, 0}};

    for (;;) {
        for (int i = 0; i < sizeof buttons / sizeof buttons[0]; i++) {
            if (gpio_get_level(buttons[i].pin) == 0) {
                buttons[i].held_ms += POLL_MS;
                continue;
            }
            // Released: act on short presses only.
            if (buttons[i].held_ms >= MIN_PRESS_MS && buttons[i].held_ms <= MAX_PRESS_MS) {
                blink();
                press_cb(buttons[i].id);
            }
            buttons[i].held_ms = 0;
        }
        vTaskDelay(pdMS_TO_TICKS(POLL_MS));
    }
}

void buttons_start(void (*on_press)(button_t button)) {
    press_cb = on_press;
    gpio_config_t in = {
        .pin_bit_mask = (1ULL << PIN_BOOT) | (1ULL << PIN_KEY),
        .mode = GPIO_MODE_INPUT,
        .pull_up_en = GPIO_PULLUP_ENABLE,
    };
    ESP_ERROR_CHECK(gpio_config(&in));
    gpio_config_t leds = {
        .pin_bit_mask = (1ULL << PIN_LED_GREEN) | (1ULL << PIN_LED_RED),
        .mode = GPIO_MODE_OUTPUT,
        .pull_up_en = GPIO_PULLUP_ENABLE,
    };
    ESP_ERROR_CHECK(gpio_config(&leds));
    gpio_set_level(PIN_LED_GREEN, 1);
    gpio_set_level(PIN_LED_RED, 1);
    xTaskCreate(poll_task, "buttons", 3 * 1024, NULL, 4, NULL);
}
