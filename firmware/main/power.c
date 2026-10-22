// AXP2101 power chip setup. The register values reproduce Waveshare's
// Custom_PmicRegisterInit (via XPowersLib) for the PhotoPainter.

#include "power.h"

#include <driver/i2c_master.h>
#include <esp_check.h>
#include <esp_log.h>

#define I2C_SDA      47
#define I2C_SCL      48
#define AXP2101_ADDR 0x34
#define AXP2101_ID   0x4A

// Registers
#define REG_IC_TYPE        0x03
#define REG_VBUS_CUR_LIMIT 0x16
#define REG_ADC_CHANNELS   0x30
#define REG_PRECHARGE_CUR  0x61
#define REG_CHARGE_CUR     0x62
#define REG_TERM_CUR       0x63
#define REG_DCDC1_VOLTAGE  0x82
#define REG_ALDO1_VOLTAGE  0x92 // ALDO2-4 follow at 0x93-0x95

// Values
#define VBUS_LIMIT_2000MA  5
#define DCDC1_3V3          ((3300 - 1500) / 100)
#define ALDO_3V3           ((3300 - 500) / 100)
#define PRECHARGE_50MA     2
#define CHARGE_500MA       11
#define TERMINATION_25MA   1

static const char *TAG = "power";

static i2c_master_dev_handle_t pmu;

static esp_err_t read_reg(uint8_t reg, uint8_t *val) {
    return i2c_master_transmit_receive(pmu, &reg, 1, val, 1, 1000);
}

static esp_err_t write_reg(uint8_t reg, uint8_t val) {
    uint8_t buf[2] = {reg, val};
    return i2c_master_transmit(pmu, buf, sizeof buf, 1000);
}

// set_bits sets the bits in mask to value, writing only if they differ.
static esp_err_t set_bits(uint8_t reg, uint8_t mask, uint8_t value) {
    uint8_t old;
    ESP_RETURN_ON_ERROR(read_reg(reg, &old), TAG, "read 0x%02x", reg);
    uint8_t val = (old & ~mask) | (value & mask);
    if (val == old) {
        return ESP_OK;
    }
    ESP_LOGI(TAG, "reg 0x%02x: 0x%02x -> 0x%02x", reg, old, val);
    return write_reg(reg, val);
}

esp_err_t power_init(void) {
    i2c_master_bus_config_t bus_cfg = {
        .i2c_port = 0,
        .sda_io_num = I2C_SDA,
        .scl_io_num = I2C_SCL,
        .clk_source = I2C_CLK_SRC_DEFAULT,
        .glitch_ignore_cnt = 7,
        .flags.enable_internal_pullup = true,
    };
    i2c_master_bus_handle_t bus;
    ESP_RETURN_ON_ERROR(i2c_new_master_bus(&bus_cfg, &bus), TAG, "I2C bus");
    i2c_device_config_t dev_cfg = {
        .dev_addr_length = I2C_ADDR_BIT_LEN_7,
        .device_address = AXP2101_ADDR,
        .scl_speed_hz = 100000,
    };
    ESP_RETURN_ON_ERROR(i2c_master_bus_add_device(bus, &dev_cfg, &pmu), TAG, "add AXP2101");

    uint8_t id;
    ESP_RETURN_ON_ERROR(read_reg(REG_IC_TYPE, &id), TAG, "read chip ID");
    if (id != AXP2101_ID) {
        ESP_LOGE(TAG, "unexpected chip ID 0x%02x, want 0x%02x", id, AXP2101_ID);
        return ESP_ERR_NOT_FOUND;
    }

    // No battery temperature sensor is fitted, so don't let it block charging.
    ESP_RETURN_ON_ERROR(set_bits(REG_ADC_CHANNELS, 0x02, 0), TAG, "TS pin");
    ESP_RETURN_ON_ERROR(set_bits(REG_VBUS_CUR_LIMIT, 0x07, VBUS_LIMIT_2000MA), TAG, "USB current limit");

    // Waveshare writes the whole DCDC1 register when its voltage isn't 3.3 V.
    uint8_t dc1;
    ESP_RETURN_ON_ERROR(read_reg(REG_DCDC1_VOLTAGE, &dc1), TAG, "read DCDC1");
    if ((dc1 & 0x1F) != DCDC1_3V3) {
        ESP_LOGI(TAG, "DCDC1 -> 3.3 V");
        ESP_RETURN_ON_ERROR(write_reg(REG_DCDC1_VOLTAGE, DCDC1_3V3), TAG, "DCDC1");
    }
    for (int i = 0; i < 4; i++) {
        ESP_RETURN_ON_ERROR(set_bits(REG_ALDO1_VOLTAGE + i, 0x1F, ALDO_3V3), TAG, "ALDO%d", i + 1);
    }

    ESP_RETURN_ON_ERROR(set_bits(REG_PRECHARGE_CUR, 0x03, PRECHARGE_50MA), TAG, "precharge current");
    ESP_RETURN_ON_ERROR(set_bits(REG_CHARGE_CUR, 0x1F, CHARGE_500MA), TAG, "charge current");
    ESP_RETURN_ON_ERROR(set_bits(REG_TERM_CUR, 0x0F, TERMINATION_25MA), TAG, "termination current");

    ESP_LOGI(TAG, "AXP2101 ready");
    return ESP_OK;
}
