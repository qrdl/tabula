idf_component_register(
    SRCS "main.c" "power.c" "epd.c" "wifi.c" "fetch.c" "buttons.c"
    INCLUDE_DIRS "."
    PRIV_REQUIRES esp_driver_gpio esp_driver_i2c esp_driver_spi esp_event esp_http_client
                  esp_http_server esp_netif esp_timer esp_wifi mbedtls nvs_flash
)
