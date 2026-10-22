#pragma once

#include <stddef.h>
#include <stdint.h>

#define FETCH_ETAG_MAX 64

typedef enum {
    FETCH_NEW,       // a new image is in buf and its ETag in new_etag
    FETCH_UNCHANGED, // the server's image matches etag
    FETCH_FAILED,
} fetch_result_t;

// fetch_image downloads <server>/api/image.bin?rotation=<rotation> into buf (len bytes).
// etag is the ETag of the image already shown ("" to always download); the server
// answers 304 if it still matches.
fetch_result_t fetch_image(uint8_t *buf, size_t len, int rotation, const char *etag, char *new_etag,
                           size_t new_etag_len);
