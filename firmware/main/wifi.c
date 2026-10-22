#include "wifi.h"

#include <string.h>

#include <esp_event.h>
#include <esp_log.h>
#include <esp_netif.h>
#include <esp_timer.h>
#include <esp_wifi.h>

#define RECONNECT_DELAY_US (5 * 1000 * 1000)

static const char *TAG = "wifi";

static void (*connected_cb)(void);
static volatile bool connected;
static esp_timer_handle_t reconnect_timer;

static void reconnect(void *arg) {
    esp_wifi_connect();
}

static void on_event(void *arg, esp_event_base_t base, int32_t id, void *data) {
    if (base == WIFI_EVENT && id == WIFI_EVENT_STA_START) {
        esp_wifi_connect();
    } else if (base == WIFI_EVENT && id == WIFI_EVENT_STA_DISCONNECTED) {
        wifi_event_sta_disconnected_t *ev = data;
        connected = false;
        ESP_LOGW(TAG, "disconnected (reason %d), retrying in %d s", ev->reason, RECONNECT_DELAY_US / 1000000);
        esp_timer_start_once(reconnect_timer, RECONNECT_DELAY_US);
    } else if (base == IP_EVENT && id == IP_EVENT_STA_GOT_IP) {
        ip_event_got_ip_t *ev = data;
        ESP_LOGI(TAG, "connected, address " IPSTR, IP2STR(&ev->ip_info.ip));
        connected = true;
        connected_cb();
    }
}

void wifi_start(void (*on_connected)(void)) {
    connected_cb = on_connected;
    if (strlen(CONFIG_TABULA_WIFI_SSID) == 0) {
        ESP_LOGE(TAG, "no Wi-Fi network set: set CONFIG_TABULA_WIFI_SSID in sdkconfig.local");
    }

    ESP_ERROR_CHECK(esp_netif_init());
    ESP_ERROR_CHECK(esp_event_loop_create_default());
    esp_netif_create_default_wifi_sta();
    wifi_init_config_t init = WIFI_INIT_CONFIG_DEFAULT();
    ESP_ERROR_CHECK(esp_wifi_init(&init));

    esp_timer_create_args_t timer = {.callback = reconnect, .name = "wifi_reconnect"};
    ESP_ERROR_CHECK(esp_timer_create(&timer, &reconnect_timer));
    ESP_ERROR_CHECK(esp_event_handler_register(WIFI_EVENT, ESP_EVENT_ANY_ID, on_event, NULL));
    ESP_ERROR_CHECK(esp_event_handler_register(IP_EVENT, IP_EVENT_STA_GOT_IP, on_event, NULL));

    wifi_config_t cfg = {0};
    strlcpy((char *)cfg.sta.ssid, CONFIG_TABULA_WIFI_SSID, sizeof cfg.sta.ssid);
    strlcpy((char *)cfg.sta.password, CONFIG_TABULA_WIFI_PASSWORD, sizeof cfg.sta.password);
    cfg.sta.threshold.authmode = strlen(CONFIG_TABULA_WIFI_PASSWORD) ? WIFI_AUTH_WPA2_PSK : WIFI_AUTH_OPEN;
    cfg.sta.sae_pwe_h2e = WPA3_SAE_PWE_BOTH;
    ESP_ERROR_CHECK(esp_wifi_set_mode(WIFI_MODE_STA));
    ESP_ERROR_CHECK(esp_wifi_set_config(WIFI_IF_STA, &cfg));
    ESP_ERROR_CHECK(esp_wifi_start());
    // The device is on USB power: answer refresh requests without power-save delays.
    ESP_ERROR_CHECK(esp_wifi_set_ps(WIFI_PS_NONE));
}

bool wifi_connected(void) {
    return connected;
}
