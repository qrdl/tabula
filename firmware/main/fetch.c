#include "fetch.h"

#include <stdio.h>
#include <string.h>
#include <strings.h>

#include <esp_crt_bundle.h>
#include <esp_http_client.h>
#include <esp_log.h>

static const char *TAG = "fetch";

static esp_err_t on_http_event(esp_http_client_event_t *evt) {
    if (evt->event_id == HTTP_EVENT_ON_HEADER && strcasecmp(evt->header_key, "ETag") == 0) {
        strlcpy(evt->user_data, evt->header_value, FETCH_ETAG_MAX);
    }
    return ESP_OK;
}

fetch_result_t fetch_image(uint8_t *buf, size_t len, int rotation, const char *etag, char *new_etag,
                           size_t new_etag_len) {
    char url[256];
    snprintf(url, sizeof url, "%s/api/image.bin?rotation=%d", CONFIG_TABULA_SERVER_URL, rotation);
    char got_etag[FETCH_ETAG_MAX] = "";
    // With a user set, the Authorization header goes with the first request (no 401 round trip).
    bool auth = CONFIG_TABULA_SERVER_USER[0] != '\0';
    esp_http_client_config_t cfg = {
        .url = url,
        .username = auth ? CONFIG_TABULA_SERVER_USER : NULL,
        .password = auth ? CONFIG_TABULA_SERVER_PASSWORD : NULL,
        .auth_type = auth ? HTTP_AUTH_TYPE_BASIC : HTTP_AUTH_TYPE_NONE,
        // An https:// server URL is checked against the built-in public CA list; http:// ignores it.
        .crt_bundle_attach = esp_crt_bundle_attach,
        .event_handler = on_http_event,
        .user_data = got_etag,
        .timeout_ms = 15000,
        .buffer_size = 4096,
    };
    esp_http_client_handle_t client = esp_http_client_init(&cfg);
    if (!client) {
        ESP_LOGE(TAG, "client init failed");
        return FETCH_FAILED;
    }
    // The header tells the server this is the device, so it records our address.
    esp_http_client_set_header(client, "X-Device", "photopainter");
    if (etag[0]) {
        esp_http_client_set_header(client, "If-None-Match", etag);
    }

    fetch_result_t result = FETCH_FAILED;
    esp_err_t err = esp_http_client_open(client, 0);
    if (err != ESP_OK) {
        ESP_LOGW(TAG, "%s: %s", url, esp_err_to_name(err));
        goto done;
    }
    int64_t content_length = esp_http_client_fetch_headers(client);
    int status = esp_http_client_get_status_code(client);
    if (status == 304) {
        ESP_LOGI(TAG, "image unchanged");
        result = FETCH_UNCHANGED;
        goto done;
    }
    if (status == 401) {
        ESP_LOGE(TAG, "%s: wrong or missing server user/password: set CONFIG_TABULA_SERVER_USER and _PASSWORD in sdkconfig.local", url);
        goto done;
    }
    if (status != 200 || content_length != (int64_t)len) {
        ESP_LOGW(TAG, "%s: status %d, %lld bytes (want %u)", url, status, content_length, (unsigned)len);
        goto done;
    }
    size_t got = 0;
    while (got < len) {
        int n = esp_http_client_read(client, (char *)buf + got, len - got);
        if (n <= 0) {
            break;
        }
        got += n;
    }
    if (got != len) {
        ESP_LOGW(TAG, "short read: %u of %u bytes", (unsigned)got, (unsigned)len);
        goto done;
    }
    strlcpy(new_etag, got_etag, new_etag_len);
    ESP_LOGI(TAG, "new image %s", new_etag);
    result = FETCH_NEW;

done:
    esp_http_client_close(client);
    esp_http_client_cleanup(client);
    return result;
}
