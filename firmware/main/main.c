// Tabula display: shows the task list image from the server and redraws it
// when the server asks (POST /refresh) or a button is pressed.

#include <stdint.h>
#include <string.h>

#include <esp_heap_caps.h>
#include <esp_http_server.h>
#include <esp_log.h>
#include <esp_timer.h>
#include <freertos/FreeRTOS.h>
#include <freertos/task.h>
#include <nvs_flash.h>

#include "buttons.h"
#include "epd.h"
#include "fetch.h"
#include "power.h"
#include "wifi.h"

#define US_PER_S             1000000LL
#define MIN_REDRAW_US        (CONFIG_TABULA_MIN_REDRAW_INTERVAL_S * US_PER_S)
#define BUTTON_MIN_REDRAW_US (CONFIG_TABULA_BUTTON_MIN_REDRAW_INTERVAL_S * US_PER_S)
#define DAILY_REDRAW_US      (CONFIG_TABULA_DAILY_REDRAW_HOURS * 3600 * US_PER_S)
#define RETRY_US             (30 * US_PER_S)
#define KEY_SETTLE_US        (3 * US_PER_S) // wait for more KEY presses before redrawing

// Notification bits for the refresh task.
#define BIT_REFRESH_REQUESTED (1 << 0)
#define BIT_WIFI_CONNECTED    (1 << 1)
#define BIT_BOOT_PRESSED      (1 << 2)
#define BIT_KEY_PRESSED       (1 << 3)

static const char *TAG = "tabula";

static TaskHandle_t refresh_task;
// KEY presses not yet handled; counted because notification bits would merge them.
static uint32_t key_presses;

static esp_err_t refresh_handler(httpd_req_t *req) {
    xTaskNotify(refresh_task, BIT_REFRESH_REQUESTED, eSetBits);
    httpd_resp_set_status(req, "202 Accepted");
    return httpd_resp_sendstr(req, "refresh queued\n");
}

static void start_http_server(void) {
    httpd_config_t cfg = HTTPD_DEFAULT_CONFIG();
    httpd_handle_t server;
    ESP_ERROR_CHECK(httpd_start(&server, &cfg));
    httpd_uri_t refresh = {.uri = "/refresh", .method = HTTP_POST, .handler = refresh_handler};
    ESP_ERROR_CHECK(httpd_register_uri_handler(server, &refresh));
}

// ticks_until converts a wait until `due` into ticks. (pdMS_TO_TICKS overflows for a day.)
static TickType_t ticks_until(int64_t due, int64_t now) {
    if (due == INT64_MAX) {
        return portMAX_DELAY;
    }
    if (due <= now) {
        return 0;
    }
    int64_t ticks = (due - now) * configTICK_RATE_HZ / US_PER_S + 1;
    return ticks >= portMAX_DELAY ? portMAX_DELAY - 1 : (TickType_t)ticks;
}

static void on_wifi_connected(void) {
    // Fetch on every (re)connect: it also tells the server our current address.
    xTaskNotify(refresh_task, BIT_WIFI_CONNECTED, eSetBits);
}

static void on_button(button_t button) {
    if (button == BUTTON_KEY) {
        __atomic_fetch_add(&key_presses, 1, __ATOMIC_SEQ_CST);
        xTaskNotify(refresh_task, BIT_KEY_PRESSED, eSetBits);
    } else {
        xTaskNotify(refresh_task, BIT_BOOT_PRESSED, eSetBits);
    }
}

// The rotation (0, 90, 180 or 270 degrees clockwise) is kept in NVS so it survives a restart.
static int load_rotation(void) {
    nvs_handle_t nvs;
    uint16_t rotation = 0;
    if (nvs_open("tabula", NVS_READONLY, &nvs) == ESP_OK) {
        nvs_get_u16(nvs, "rotation", &rotation);
        nvs_close(nvs);
    }
    return rotation < 360 && rotation % 90 == 0 ? rotation : 0;
}

static void save_rotation(int rotation) {
    nvs_handle_t nvs;
    esp_err_t err = nvs_open("tabula", NVS_READWRITE, &nvs);
    if (err == ESP_OK) {
        err = nvs_set_u16(nvs, "rotation", rotation);
        if (err == ESP_OK) {
            err = nvs_commit(nvs);
        }
        nvs_close(nvs);
    }
    if (err != ESP_OK) {
        ESP_LOGW(TAG, "saving rotation: %s", esp_err_to_name(err));
    }
}

// refresh_loop decides when to download and redraw:
//   - a download is wanted after boot, after a refresh request, after Wi-Fi reconnects
//     and after a button press;
//   - redraws are at least MIN_REDRAW_US apart, or BUTTON_MIN_REDRAW_US after a button press;
//     a request in between waits, it isn't dropped;
//   - BOOT redraws even if the image hasn't changed; KEY turns the image 90° and waits
//     KEY_SETTLE_US for further presses;
//   - the image is redrawn at least every DAILY_REDRAW_US, as Waveshare's manual advises;
//   - a failed download is retried after RETRY_US.
static void refresh_loop(void *arg) {
    uint8_t *shown = heap_caps_malloc(EPD_IMAGE_BYTES, MALLOC_CAP_SPIRAM);
    uint8_t *incoming = heap_caps_malloc(EPD_IMAGE_BYTES, MALLOC_CAP_SPIRAM);
    assert(shown && incoming);
    char etag[FETCH_ETAG_MAX] = "";
    int rotation = load_rotation();
    ESP_LOGI(TAG, "rotation %d", rotation);

    bool fetch_wanted = true;
    bool by_button = false; // the wanted download comes from a button: the shorter pause applies
    bool force = false;     // redraw even if the image hasn't changed
    bool drawn = false;     // whether shown holds an image on the panel
    int64_t last_draw = 0;  // when the last redraw finished
    int64_t next_try = 0;   // earliest time for the next download attempt
    int64_t settle_until = 0;

    for (;;) {
        int64_t now = esp_timer_get_time();
        int64_t due = INT64_MAX;
        if (fetch_wanted) {
            int64_t pause = by_button ? BUTTON_MIN_REDRAW_US : MIN_REDRAW_US;
            due = next_try > settle_until ? next_try : settle_until;
            if (drawn && last_draw + pause > due) {
                due = last_draw + pause;
            }
        } else if (drawn) {
            due = last_draw + DAILY_REDRAW_US;
        }

        uint32_t bits = 0;
        if (xTaskNotifyWait(0, UINT32_MAX, &bits, ticks_until(due, now)) == pdTRUE) {
            if (bits & BIT_REFRESH_REQUESTED) {
                ESP_LOGI(TAG, "refresh requested");
            }
            if (bits & BIT_WIFI_CONNECTED) {
                next_try = 0;
            }
            if (bits & BIT_BOOT_PRESSED) {
                ESP_LOGI(TAG, "BOOT pressed: refresh");
                by_button = force = true;
            }
            uint32_t presses = __atomic_exchange_n(&key_presses, 0, __ATOMIC_SEQ_CST);
            if (presses) {
                rotation = (rotation + 90 * presses) % 360;
                save_rotation(rotation);
                ESP_LOGI(TAG, "KEY pressed: rotation %d", rotation);
                by_button = true;
                settle_until = esp_timer_get_time() + KEY_SETTLE_US;
            }
            fetch_wanted = true;
            continue; // work out the new due time
        }

        now = esp_timer_get_time();
        if (now < due) {
            continue;
        }
        if (!fetch_wanted) {
            ESP_LOGI(TAG, "daily redraw");
            epd_show(shown);
            last_draw = esp_timer_get_time();
            continue;
        }
        if (!wifi_connected()) {
            next_try = now + RETRY_US; // the reconnect notification will wake us sooner
            continue;
        }

        char new_etag[FETCH_ETAG_MAX];
        switch (fetch_image(incoming, EPD_IMAGE_BYTES, rotation, force ? "" : etag, new_etag, sizeof new_etag)) {
        case FETCH_NEW: {
            uint8_t *t = shown;
            shown = incoming;
            incoming = t;
            strlcpy(etag, new_etag, sizeof etag);
            epd_show(shown);
            drawn = true;
            last_draw = esp_timer_get_time();
            fetch_wanted = by_button = force = false;
            break;
        }
        case FETCH_UNCHANGED:
            fetch_wanted = by_button = false;
            break;
        case FETCH_FAILED:
            ESP_LOGW(TAG, "download failed, retrying in %lld s", RETRY_US / US_PER_S);
            next_try = now + RETRY_US;
            break;
        }
    }
}

void app_main(void) {
    esp_err_t err = nvs_flash_init(); // needed by Wi-Fi and the saved rotation
    if (err == ESP_ERR_NVS_NO_FREE_PAGES || err == ESP_ERR_NVS_NEW_VERSION_FOUND) {
        ESP_ERROR_CHECK(nvs_flash_erase());
        err = nvs_flash_init();
    }
    ESP_ERROR_CHECK(err);

    if (power_init() != ESP_OK) {
        ESP_LOGE(TAG, "power chip setup failed; the screen may not work");
    }
    epd_init();

    xTaskCreate(refresh_loop, "refresh", 6 * 1024, NULL, 5, &refresh_task);
    buttons_start(on_button);
    wifi_start(on_wifi_connected);
    start_http_server();
}
