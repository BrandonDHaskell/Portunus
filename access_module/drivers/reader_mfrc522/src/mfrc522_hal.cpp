/**
 * @file mfrc522_hal.cpp
 * @brief MFRC522 HAL implementation — private to reader_mfrc522.
 *
 * Register-level SPI driver. The ISO 14443A card protocol lives in
 * iso14443a.cpp; this file supplies its transceive and CRC_A operations.
 */

#include "mfrc522.hpp"
#include "iso14443a.hpp"
#include "mfrc522_status.hpp"
#include "pin_config.hpp"
#include "error_codes.hpp"

#include "driver/spi_master.h"
#include "driver/gpio.h"
#include "esp_log.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"

#include <string.h>

static const char *TAG = "mfrc522";

/* ── MFRC522 register addresses ────────────────────────────────────────────── */

/* Command and status registers */
#define REG_COMMAND        0x01
#define REG_COM_I_EN       0x02
#define REG_DIV_I_EN       0x03
#define REG_COM_IRQ        0x04
#define REG_DIV_IRQ        0x05
#define REG_ERROR          0x06
#define REG_STATUS1        0x07
#define REG_STATUS2        0x08
#define REG_FIFO_DATA      0x09
#define REG_FIFO_LEVEL     0x0A
#define REG_WATER_LEVEL    0x0B
#define REG_CONTROL        0x0C
#define REG_BIT_FRAMING    0x0D
#define REG_COLL           0x0E

/* Communication registers */
#define REG_MODE           0x11
#define REG_TX_MODE        0x12
#define REG_RX_MODE        0x13
#define REG_TX_CONTROL     0x14
#define REG_TX_ASK         0x15

/* Configuration registers */
#define REG_CRC_RESULT_H   0x21
#define REG_CRC_RESULT_L   0x22
#define REG_MOD_WIDTH      0x24
#define REG_RF_CFG         0x26
#define REG_T_MODE         0x2A
#define REG_T_PRESCALER    0x2B
#define REG_T_RELOAD_H     0x2C
#define REG_T_RELOAD_L     0x2D

/* Test registers */
#define REG_VERSION        0x37

/* ── MFRC522 commands ──────────────────────────────────────────────────────── */

#define CMD_IDLE           0x00
#define CMD_CALC_CRC       0x03
#define CMD_TRANSCEIVE     0x0C
#define CMD_MF_AUTHENT     0x0E
#define CMD_SOFT_RESET     0x0F

/* ── SPI configuration ─────────────────────────────────────────────────────── */

#define MFRC522_SPI_CLOCK_HZ  5000000   /* 5 MHz — well within MFRC522 max of 10 MHz */

/* ── Module state ──────────────────────────────────────────────────────────── */

static spi_device_handle_t s_spi_handle     = NULL;
static bool                s_spi_error      = false; /* set by reg_read on transfer failure */
static bool                s_spi_initialized = false; /* SPI bus + device added; skip on re-init */

/* ── Low-level SPI register access ─────────────────────────────────────────── */

/**
 * SPI framing (MFRC522 datasheet §8.1.2):
 *   Byte 0: address byte — (reg << 1) | 0x80 for read, (reg << 1) & 0x7E for write
 *   Byte 1: data (write) or 0x00 (read, returns data on MISO)
 */
static uint8_t reg_read(uint8_t reg)
{
    uint8_t tx[2] = { (uint8_t)(((reg & 0x3F) << 1) | 0x80), 0x00 };
    uint8_t rx[2] = { 0 };

    spi_transaction_t txn = {};
    txn.length    = 16;     /* 2 bytes = 16 bits */
    txn.tx_buffer = tx;
    txn.rx_buffer = rx;

    esp_err_t err = spi_device_transmit(s_spi_handle, &txn);
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "SPI read reg 0x%02x failed: %s", reg, esp_err_to_name(err));
        s_spi_error = true;
        return 0;
    }
    return rx[1];
}

static void reg_write(uint8_t reg, uint8_t value)
{
    uint8_t tx[2] = { (uint8_t)((reg & 0x3F) << 1), value };

    spi_transaction_t txn = {};
    txn.length    = 16;
    txn.tx_buffer = tx;

    esp_err_t err = spi_device_transmit(s_spi_handle, &txn);
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "SPI write reg 0x%02x failed: %s", reg, esp_err_to_name(err));
    }
}

/** Set specific bits in a register. */
static void reg_set_bits(uint8_t reg, uint8_t mask)
{
    reg_write(reg, reg_read(reg) | mask);
}

/** Clear specific bits in a register. */
static void reg_clear_bits(uint8_t reg, uint8_t mask)
{
    reg_write(reg, reg_read(reg) & ~mask);
}

/* ── iso14443a_io_t operations ─────────────────────────────────────────────── */

/**
 * @brief Execute a Transceive command and wait for completion.
 *
 * Implements iso14443a_io_t::transceive: sends @p tx_len bytes, waits for
 * the card's reply or the timer, and copies up to @p *rx_len received bytes
 * into @p rx.
 */
static portunus_err_t hal_transceive(void * /*ctx*/, const uint8_t *tx, uint8_t tx_len,
                                     uint8_t tx_last_bits, uint8_t *rx, uint8_t *rx_len)
{
    s_spi_error = false;

    reg_write(REG_COMMAND, CMD_IDLE);          /* Stop any active command */
    reg_write(REG_COM_IRQ, 0x7F);             /* Clear all interrupt flags */
    reg_write(REG_FIFO_LEVEL, 0x80);          /* Flush FIFO */

    for (uint8_t i = 0; i < tx_len; i++) {
        reg_write(REG_FIFO_DATA, tx[i]);
    }

    reg_write(REG_BIT_FRAMING, tx_last_bits & 0x07); /* Valid bits in last tx byte */
    reg_write(REG_COMMAND, CMD_TRANSCEIVE);
    reg_set_bits(REG_BIT_FRAMING, 0x80);      /* StartSend=1: start transmission */

    uint16_t timeout_loops = 2000;
    uint8_t irq;
    do {
        irq = reg_read(REG_COM_IRQ);
        if (s_spi_error) {
            return PORTUNUS_ERR_SPI_TRANSFER;
        }
        if (--timeout_loops == 0) {
            ESP_LOGD(TAG, "Transceive timeout");
            return PORTUNUS_ERR_TIMEOUT;
        }
    } while (!(irq & MFRC522_IRQ_DONE));

    /* ErrorReg is only meaningful when the timer did not fire. */
    const uint8_t error_reg = (irq & MFRC522_IRQ_TIMER) ? 0 : reg_read(REG_ERROR);
    if (s_spi_error) {
        return PORTUNUS_ERR_SPI_TRANSFER;
    }
    const portunus_err_t status = mfrc522_transceive_status(irq, error_reg);
    if (status != PORTUNUS_OK) {
        if (status != PORTUNUS_ERR_NO_CREDENTIAL) {
            ESP_LOGD(TAG, "Transceive error: 0x%02x", error_reg);
        }
        return status;
    }

    if (rx && rx_len) {
        uint8_t n = reg_read(REG_FIFO_LEVEL);
        if (n > *rx_len) {
            n = *rx_len;
        }
        *rx_len = n;
        for (uint8_t i = 0; i < n; i++) {
            rx[i] = reg_read(REG_FIFO_DATA);
        }
    }

    return PORTUNUS_OK;
}

/**
 * @brief Calculate CRC_A using the MFRC522 coprocessor.
 *
 * Implements iso14443a_io_t::crc_a.
 *
 * @return PORTUNUS_OK on success, PORTUNUS_ERR_TIMEOUT if the CRC unit stalls.
 */
static portunus_err_t hal_crc_a(void * /*ctx*/, const uint8_t *data, uint8_t len,
                                uint8_t out[2])
{
    reg_write(REG_COMMAND, CMD_IDLE);
    reg_write(REG_DIV_IRQ, 0x04);        /* Clear CRCIRq */
    reg_write(REG_FIFO_LEVEL, 0x80);     /* Flush FIFO */

    for (uint8_t i = 0; i < len; i++) {
        reg_write(REG_FIFO_DATA, data[i]);
    }

    reg_write(REG_COMMAND, CMD_CALC_CRC);

    uint16_t timeout = 5000;
    while (!(reg_read(REG_DIV_IRQ) & 0x04)) {
        if (--timeout == 0) {
            return PORTUNUS_ERR_TIMEOUT;
        }
    }

    out[0] = reg_read(REG_CRC_RESULT_L);
    out[1] = reg_read(REG_CRC_RESULT_H);

    return PORTUNUS_OK;
}

static const iso14443a_io_t s_io = { nullptr, hal_transceive, hal_crc_a };

/* ── Public API ────────────────────────────────────────────────────────────── */

portunus_err_t mfrc522_init(void)
{
    /* ── RST pin: configure on first call, toggle on every call ─────────── */
    if (PIN_MFRC522_RST >= 0) {
        if (!s_spi_initialized) {
            gpio_config_t rst_cfg = {};
            rst_cfg.pin_bit_mask = (1ULL << PIN_MFRC522_RST);
            rst_cfg.mode         = GPIO_MODE_OUTPUT;
            rst_cfg.pull_up_en   = GPIO_PULLUP_DISABLE;
            rst_cfg.pull_down_en = GPIO_PULLDOWN_DISABLE;
            rst_cfg.intr_type    = GPIO_INTR_DISABLE;
            gpio_config(&rst_cfg);
        }
        gpio_set_level((gpio_num_t)PIN_MFRC522_RST, 0);
        vTaskDelay(pdMS_TO_TICKS(10));
        gpio_set_level((gpio_num_t)PIN_MFRC522_RST, 1);
        vTaskDelay(pdMS_TO_TICKS(50));
    }

    /* ── SPI bus and device: initialise once, reuse on recovery calls ─────── */
    if (!s_spi_initialized) {
        spi_bus_config_t bus_cfg = {};
        bus_cfg.mosi_io_num     = PIN_SPI_MOSI;
        bus_cfg.miso_io_num     = PIN_SPI_MISO;
        bus_cfg.sclk_io_num     = PIN_SPI_SCLK;
        bus_cfg.quadwp_io_num   = -1;
        bus_cfg.quadhd_io_num   = -1;
        bus_cfg.max_transfer_sz = 64;

        esp_err_t ret = spi_bus_initialize(MFRC522_SPI_HOST, &bus_cfg, SPI_DMA_CH_AUTO);
        if (ret != ESP_OK) {
            ESP_LOGE(TAG, "SPI bus init failed: %s", esp_err_to_name(ret));
            return PORTUNUS_ERR_SPI_INIT;
        }

        spi_device_interface_config_t dev_cfg = {};
        dev_cfg.clock_speed_hz = MFRC522_SPI_CLOCK_HZ;
        dev_cfg.mode           = 0;          /* CPOL=0, CPHA=0 */
        dev_cfg.spics_io_num   = PIN_MFRC522_CS;
        dev_cfg.queue_size     = 4;

        ret = spi_bus_add_device(MFRC522_SPI_HOST, &dev_cfg, &s_spi_handle);
        if (ret != ESP_OK) {
            ESP_LOGE(TAG, "SPI add device failed: %s", esp_err_to_name(ret));
            spi_bus_free(MFRC522_SPI_HOST);
            return PORTUNUS_ERR_SPI_INIT;
        }

        s_spi_initialized = true;
    }

    /* ── Soft reset ──────────────────────────────────────────────────────── */
    reg_write(REG_COMMAND, CMD_SOFT_RESET);
    vTaskDelay(pdMS_TO_TICKS(50));

    /* Wait for the oscillator to start (PowerDown bit in CommandReg clears) */
    uint16_t attempts = 100;
    while ((reg_read(REG_COMMAND) & 0x10) && --attempts) {
        vTaskDelay(pdMS_TO_TICKS(10));
    }
    if (attempts == 0) {
        ESP_LOGE(TAG, "MFRC522 did not come out of reset");
        return PORTUNUS_ERR_DEVICE_NOT_FOUND;
    }

    /* ── Default register configuration ──────────────────────────────────── */
    /* Timer: auto-start on end of transmission, prescaler → ~25 ms timeout */
    reg_write(REG_T_MODE,      0x8D);   /* TAuto=1, TPrescaler[11:8]=0x0D */
    reg_write(REG_T_PRESCALER, 0x3E);   /* TPrescaler[7:0]=0x3E  →  total 0xD3E */
    reg_write(REG_T_RELOAD_H,  0x00);   /* TReload = 30 */
    reg_write(REG_T_RELOAD_L,  0x1E);
    reg_write(REG_TX_ASK,      0x40);   /* Force 100% ASK modulation */
    reg_write(REG_MODE,        0x3D);   /* CRC preset 0x6363 (ISO 14443-3) */

    /* Receiver gain: maximum (48 dB) for reliable reads on breadboard setups */
    reg_write(REG_RF_CFG, 0x70);

    /* ── Verify communication ────────────────────────────────────────────── */
    uint8_t version = mfrc522_get_version();
    if (mfrc522_version_is_dead(version)) {
        ESP_LOGE(TAG, "MFRC522 not detected (version=0x%02x). Check wiring.", version);
        return PORTUNUS_ERR_DEVICE_NOT_FOUND;
    }
    ESP_LOGI(TAG, "MFRC522 detected, version=0x%02x", version);

    mfrc522_antenna_on();

    return PORTUNUS_OK;
}

portunus_err_t mfrc522_read_credential(credential_t *cred)
{
    reg_clear_bits(REG_COLL, 0x80);  /* ValuesAfterColl=0: clear bits received after a collision */
    const portunus_err_t err = iso14443a_read_uid(s_io, cred);

    /* A disconnected chip whose MISO floats high reads 0xFF from every
     * register, including the timer IRQ bit, so it looks exactly like an
     * empty field and would never be reported. One VersionReg read per idle
     * poll tells the two apart and lets the reader-health policy degrade. */
    if (err == PORTUNUS_ERR_NO_CREDENTIAL) {
        const uint8_t version = mfrc522_get_version();
        if (mfrc522_version_is_dead(version)) {
            ESP_LOGD(TAG, "No card, and VersionReg reads 0x%02x: chip not answering", version);
            return PORTUNUS_ERR_DEVICE_NOT_FOUND;
        }
    }
    return err;
}

uint8_t mfrc522_get_version(void)
{
    return reg_read(REG_VERSION);
}

void mfrc522_halt_credential(void)
{
    iso14443a_halt(s_io);
}

void mfrc522_antenna_on(void)
{
    uint8_t val = reg_read(REG_TX_CONTROL);
    if ((val & 0x03) != 0x03) {
        reg_write(REG_TX_CONTROL, val | 0x03);
    }
}

void mfrc522_antenna_off(void)
{
    reg_clear_bits(REG_TX_CONTROL, 0x03);
}