#pragma once

#include <stdint.h>

// The 7.3" Spectra 6 panel: 800×480, 4 bits per pixel, left pixel in the high bits.
#define EPD_WIDTH       800
#define EPD_HEIGHT      480
#define EPD_IMAGE_BYTES (EPD_WIDTH * EPD_HEIGHT / 2)

// epd_init sets up SPI and the pins. The panel stays asleep until epd_show.
void epd_init(void);

// epd_show wakes the panel, sends an image of EPD_IMAGE_BYTES bytes, refreshes
// it and puts the panel back into deep sleep. It takes about 25-30 seconds.
void epd_show(const uint8_t *image);
