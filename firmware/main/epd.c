// E-paper driver. Pins and command sequences are from Waveshare's
// PhotoPainter code (components/port_bsp/display_bsp.cpp).

#include "epd.h"

#include <string.h>

#include <driver/gpio.h>
#include <driver/spi_master.h>
#include <esp_heap_caps.h>
#include <esp_log.h>
#include <esp_timer.h>
#include <freertos/FreeRTOS.h>
#include <freertos/task.h>

#define PIN_MOSI 11
#define PIN_SCLK 10
#define PIN_DC   8
#define PIN_CS   9
#define PIN_RST  12
#define PIN_BUSY 13 // high when the panel is idle

#define SPI_HOST_ID     SPI3_HOST
#define CHUNK_BYTES     4096
#define BUSY_TIMEOUT_MS 60000

static const char *TAG = "epd";

static spi_device_handle_t spi;
static uint8_t *chunk; // DMA-capable bounce buffer; the image itself lives in PSRAM

static void spi_write(const uint8_t *data, size_t len) {
    spi_transaction_t t = {
        .length = len * 8,
        .tx_buffer = data,
    };
    ESP_ERROR_CHECK(spi_device_polling_transmit(spi, &t));
}

static void send_command(uint8_t cmd) {
    gpio_set_level(PIN_DC, 0);
    gpio_set_level(PIN_CS, 0);
    spi_write(&cmd, 1);
    gpio_set_level(PIN_CS, 1);
}

static void send_data(uint8_t data) {
    gpio_set_level(PIN_DC, 1);
    gpio_set_level(PIN_CS, 0);
    spi_write(&data, 1);
    gpio_set_level(PIN_CS, 1);
}

static void send(uint8_t cmd, const uint8_t *data, size_t len) {
    send_command(cmd);
    for (size_t i = 0; i < len; i++) {
        send_data(data[i]);
    }
}

static void wait_idle(void) {
    for (int waited = 0; !gpio_get_level(PIN_BUSY); waited += 10) {
        if (waited >= BUSY_TIMEOUT_MS) {
            ESP_LOGE(TAG, "panel still busy after %d ms", BUSY_TIMEOUT_MS);
            return;
        }
        vTaskDelay(pdMS_TO_TICKS(10));
    }
}

static void reset(void) {
    gpio_set_level(PIN_RST, 1);
    vTaskDelay(pdMS_TO_TICKS(50));
    gpio_set_level(PIN_RST, 0);
    vTaskDelay(pdMS_TO_TICKS(20));
    gpio_set_level(PIN_RST, 1);
    vTaskDelay(pdMS_TO_TICKS(50));
}

void epd_init(void) {
    spi_bus_config_t bus = {
        .mosi_io_num = PIN_MOSI,
        .miso_io_num = -1,
        .sclk_io_num = PIN_SCLK,
        .quadwp_io_num = -1,
        .quadhd_io_num = -1,
        .max_transfer_sz = CHUNK_BYTES,
    };
    ESP_ERROR_CHECK(spi_bus_initialize(SPI_HOST_ID, &bus, SPI_DMA_CH_AUTO));
    spi_device_interface_config_t dev = {
        .clock_speed_hz = 40 * 1000 * 1000,
        .mode = 0,
        .spics_io_num = -1, // CS is driven by hand so it can stay low across chunks
        .queue_size = 7,
        .flags = SPI_DEVICE_HALFDUPLEX,
    };
    ESP_ERROR_CHECK(spi_bus_add_device(SPI_HOST_ID, &dev, &spi));

    gpio_config_t out = {
        .pin_bit_mask = (1ULL << PIN_RST) | (1ULL << PIN_DC) | (1ULL << PIN_CS),
        .mode = GPIO_MODE_OUTPUT,
        .pull_up_en = GPIO_PULLUP_ENABLE,
    };
    ESP_ERROR_CHECK(gpio_config(&out));
    gpio_config_t in = {
        .pin_bit_mask = 1ULL << PIN_BUSY,
        .mode = GPIO_MODE_INPUT,
        .pull_up_en = GPIO_PULLUP_ENABLE,
    };
    ESP_ERROR_CHECK(gpio_config(&in));
    gpio_set_level(PIN_CS, 1);

    chunk = heap_caps_malloc(CHUNK_BYTES, MALLOC_CAP_DMA | MALLOC_CAP_INTERNAL);
    assert(chunk);
    // The panel itself stays untouched (asleep) until the first redraw.
}

// wake resets the panel, which brings it out of deep sleep, and configures it.
static void wake(void) {
    reset();
    wait_idle();
    vTaskDelay(pdMS_TO_TICKS(50));

    send(0xAA, (const uint8_t[]){0x49, 0x55, 0x20, 0x08, 0x09, 0x18}, 6);
    send(0x01, (const uint8_t[]){0x3F}, 1);
    send(0x00, (const uint8_t[]){0x5F, 0x69}, 2);
    send(0x03, (const uint8_t[]){0x00, 0x54, 0x00, 0x44}, 4);
    send(0x05, (const uint8_t[]){0x40, 0x1F, 0x1F, 0x2C}, 4);
    send(0x06, (const uint8_t[]){0x6F, 0x1F, 0x17, 0x49}, 4);
    send(0x08, (const uint8_t[]){0x6F, 0x1F, 0x1F, 0x22}, 4);
    send(0x30, (const uint8_t[]){0x03}, 1);
    send(0x50, (const uint8_t[]){0x3F}, 1);
    send(0x60, (const uint8_t[]){0x02, 0x00}, 2);
    send(0x61, (const uint8_t[]){0x03, 0x20, 0x01, 0xE0}, 4); // 800 × 480
    send(0x84, (const uint8_t[]){0x01}, 1);
    send(0xE3, (const uint8_t[]){0x2F}, 1);

    send_command(0x04); // power on
    wait_idle();
}

// deep_sleep powers the panel down. Waveshare's manual warns that leaving it
// powered damages it; the sequence is their EPD_7IN3E_Sleep.
static void deep_sleep(void) {
    send(0x02, (const uint8_t[]){0x00}, 1); // power off
    wait_idle();
    send(0x07, (const uint8_t[]){0xA5}, 1); // deep sleep
}

void epd_show(const uint8_t *image) {
    int64_t start = esp_timer_get_time();

    wake();
    send_command(0x10); // image data
    gpio_set_level(PIN_DC, 1);
    gpio_set_level(PIN_CS, 0);
    for (size_t off = 0; off < EPD_IMAGE_BYTES; off += CHUNK_BYTES) {
        size_t n = EPD_IMAGE_BYTES - off < CHUNK_BYTES ? EPD_IMAGE_BYTES - off : CHUNK_BYTES;
        memcpy(chunk, image + off, n);
        spi_write(chunk, n);
    }
    gpio_set_level(PIN_CS, 1);

    send_command(0x04); // power on
    wait_idle();
    send(0x06, (const uint8_t[]){0x6F, 0x1F, 0x17, 0x49}, 4);
    send(0x12, (const uint8_t[]){0x00}, 1); // refresh
    wait_idle();
    deep_sleep();

    ESP_LOGI(TAG, "redraw took %lld ms", (esp_timer_get_time() - start) / 1000);
}
