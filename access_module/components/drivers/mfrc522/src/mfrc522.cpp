/**
 * @file mfrc522.cpp
 * @brief MFRC522 RFID reader driver implementation.
 *
 * Register-level SPI driver implementing ISO 14443A card detection,
 * anti-collision cascade levels 1 and 2, and UID extraction.
 */

#include "mfrc522.h"
#include "pin_config.h"
#include "error_codes.h"

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

/* ── ISO 14443A PICC commands ──────────────────────────────────────────────── */

#define PICC_REQA          0x26
#define PICC_WUPA          0x52
#define PICC_SEL_CL1       0x93
#define PICC_SEL_CL2       0x95
#define PICC_SEL_CL3       0x97
#define PICC_HLTA          0x50
#define PICC_CASCADE_TAG   0x88

/* ── IRQ bit masks ─────────────────────────────────────────────────────────── */

#define IRQ_RX_DONE        0x20
#define IRQ_IDLE           0x10
#define IRQ_ERR            0x02
#define IRQ_TIMER          0x01

/* ── SPI configuration ─────────────────────────────────────────────────────── */

#define MFRC522_SPI_CLOCK_HZ  5000000   /* 5 MHz — well within MFRC522 max of 10 MHz */

/* ── Module state ──────────────────────────────────────────────────────────── */

static spi_device_handle_t s_spi_handle = NULL;

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

/* ── Internal helpers ──────────────────────────────────────────────────────── */

/**
 * @brief Execute a Transceive command and wait for completion.
 *
 * Sends @p send_len bytes from @p send_buf via the antenna and waits for
 * a response. Received data is written to @p recv_buf (up to @p *recv_len
 * bytes); actual receive length is written back to @p *recv_len.
 *
 * @param send_buf     Data to transmit.
 * @param send_len     Number of bytes to transmit.
 * @param recv_buf     Buffer for received data (may be NULL if no response expected).
 * @param recv_len     [in/out] Max receive length → actual received length.
 * @param valid_bits   Number of valid bits in the last byte for short frames
 *                     (0 = all 8 bits valid). Updated with received last-byte valid bits.
 * @return PORTUNUS_OK on success.
 */
static portunus_err_t transceive(const uint8_t *send_buf, uint8_t send_len,
                                  uint8_t *recv_buf, uint8_t *recv_len,
                                  uint8_t *valid_bits)
{
    uint8_t tx_last_bits = valid_bits ? (*valid_bits & 0x07) : 0;

    reg_write(REG_COMMAND, CMD_IDLE);          /* Stop any active command */
    reg_write(REG_COM_IRQ, 0x7F);             /* Clear all interrupt flags */
    reg_write(REG_FIFO_LEVEL, 0x80);          /* Flush FIFO */

    /* Write data to FIFO */
    for (uint8_t i = 0; i < send_len; i++) {
        reg_write(REG_FIFO_DATA, send_buf[i]);
    }

    reg_write(REG_BIT_FRAMING, tx_last_bits); /* Set number of valid bits in last tx byte */
    reg_write(REG_COMMAND, CMD_TRANSCEIVE);   /* Execute Transceive */
    reg_set_bits(REG_BIT_FRAMING, 0x80);      /* StartSend=1 — start transmission */

    /* Poll for completion (RxIRq, IdleIRq, TimerIRq, or ErrIRq) */
    uint16_t timeout_loops = 2000;
    uint8_t irq;
    do {
        irq = reg_read(REG_COM_IRQ);
        if (--timeout_loops == 0) {
            ESP_LOGD(TAG, "Transceive timeout");
            return PORTUNUS_ERR_TIMEOUT;
        }
    } while (!(irq & (IRQ_RX_DONE | IRQ_IDLE | IRQ_ERR | IRQ_TIMER)));

    /* Check for timer timeout (no card present) */
    if (irq & IRQ_TIMER) {
        return PORTUNUS_ERR_NO_CARD;
    }

    /* Check for errors */
    uint8_t error_reg = reg_read(REG_ERROR);
    if (error_reg & 0x13) {  /* BufferOvfl | ParityErr | ProtocolErr */
        ESP_LOGD(TAG, "Transceive error: 0x%02x", error_reg);
        if (error_reg & 0x08) {  /* CollErr */
            return PORTUNUS_ERR_CARD_COLLISION;
        }
        return PORTUNUS_ERR_CARD_READ;
    }

    /* Read received data from FIFO */
    if (recv_buf && recv_len) {
        uint8_t n = reg_read(REG_FIFO_LEVEL);
        if (n > *recv_len) {
            n = *recv_len;
        }
        *recv_len = n;
        for (uint8_t i = 0; i < n; i++) {
            recv_buf[i] = reg_read(REG_FIFO_DATA);
        }
        if (valid_bits) {
            *valid_bits = reg_read(REG_CONTROL) & 0x07;
        }
    }

    return PORTUNUS_OK;
}

/**
 * @brief Send REQA (Request command Type A) to detect cards in the field.
 *
 * @param[out] atqa   2-byte ATQA response from the card.
 * @return PORTUNUS_OK if a card responded.
 */
static portunus_err_t picc_request(uint8_t *atqa)
{
    reg_write(REG_BIT_FRAMING, 0x00);
    reg_clear_bits(REG_COLL, 0x80);  /* ValuesAfterColl=0 — all received bits are valid */

    uint8_t cmd = PICC_REQA;
    uint8_t recv_len = 2;
    uint8_t valid_bits = 7;  /* REQA is a short frame: 7 bits */

    portunus_err_t err = transceive(&cmd, 1, atqa, &recv_len, &valid_bits);
    if (err != PORTUNUS_OK) {
        return err;
    }
    if (recv_len != 2) {
        return PORTUNUS_ERR_CARD_READ;
    }

    return PORTUNUS_OK;
}

/**
 * @brief Perform anti-collision and select for one cascade level.
 *
 * @param sel_cmd       Cascade level command (PICC_SEL_CL1/CL2/CL3).
 * @param[out] uid_part 4 UID bytes + 1 BCC byte (5 bytes total).
 * @return PORTUNUS_OK on success.
 */
static portunus_err_t picc_anticoll_select(uint8_t sel_cmd, uint8_t *uid_part)
{
    /* Anti-collision: SEL + NVB(0x20 = 2 valid bytes, 0 bits) */
    uint8_t buf[9];
    buf[0] = sel_cmd;
    buf[1] = 0x20;  /* NVB: 2 complete bytes sent (SEL + NVB only) */

    uint8_t recv_len = 5;
    uint8_t valid_bits = 0;
    portunus_err_t err = transceive(buf, 2, uid_part, &recv_len, &valid_bits);
    if (err != PORTUNUS_OK) {
        return err;
    }
    if (recv_len != 5) {
        return PORTUNUS_ERR_CARD_READ;
    }

    /* Verify BCC (uid[0] ^ uid[1] ^ uid[2] ^ uid[3] == uid[4]) */
    uint8_t bcc = uid_part[0] ^ uid_part[1] ^ uid_part[2] ^ uid_part[3];
    if (bcc != uid_part[4]) {
        ESP_LOGW(TAG, "BCC check failed: computed 0x%02x, received 0x%02x", bcc, uid_part[4]);
        return PORTUNUS_ERR_CARD_READ;
    }

    /* Select: SEL + NVB(0x70 = 7 valid bytes) + 4 UID + BCC + CRC_A */
    buf[0] = sel_cmd;
    buf[1] = 0x70;  /* NVB: 7 complete bytes */
    memcpy(&buf[2], uid_part, 5);  /* 4 UID bytes + BCC */

    /* Calculate CRC_A and append */
    reg_write(REG_COMMAND, CMD_IDLE);
    reg_write(REG_DIV_IRQ, 0x04);     /* Clear CRCIRq */
    reg_write(REG_FIFO_LEVEL, 0x80);  /* Flush FIFO */
    for (int i = 0; i < 7; i++) {
        reg_write(REG_FIFO_DATA, buf[i]);
    }
    reg_write(REG_COMMAND, CMD_CALC_CRC);

    uint16_t crc_timeout = 5000;
    while (!(reg_read(REG_DIV_IRQ) & 0x04)) {
        if (--crc_timeout == 0) {
            return PORTUNUS_ERR_TIMEOUT;
        }
    }
    buf[7] = reg_read(REG_CRC_RESULT_L);
    buf[8] = reg_read(REG_CRC_RESULT_H);

    /* Send select with CRC */
    uint8_t sak[3];  /* SAK + CRC_A (3 bytes) */
    recv_len = 3;
    valid_bits = 0;
    err = transceive(buf, 9, sak, &recv_len, &valid_bits);
    if (err != PORTUNUS_OK) {
        return err;
    }

    /* SAK bit 2 (0x04) indicates UID not complete — cascade needed */
    if (sak[0] & 0x04) {
        ESP_LOGD(TAG, "Cascade bit set in SAK — continuing to next level");
    }

    return PORTUNUS_OK;
}

/* ── Public API ────────────────────────────────────────────────────────────── */

portunus_err_t mfrc522_init(void)
{
    /* ── Configure RST pin and perform hardware reset ────────────────────── */
    if (PIN_MFRC522_RST >= 0) {
        gpio_config_t rst_cfg = {};
        rst_cfg.pin_bit_mask = (1ULL << PIN_MFRC522_RST);
        rst_cfg.mode         = GPIO_MODE_OUTPUT;
        rst_cfg.pull_up_en   = GPIO_PULLUP_DISABLE;
        rst_cfg.pull_down_en = GPIO_PULLDOWN_DISABLE;
        rst_cfg.intr_type    = GPIO_INTR_DISABLE;
        gpio_config(&rst_cfg);

        gpio_set_level((gpio_num_t)PIN_MFRC522_RST, 0);
        vTaskDelay(pdMS_TO_TICKS(10));
        gpio_set_level((gpio_num_t)PIN_MFRC522_RST, 1);
        vTaskDelay(pdMS_TO_TICKS(50));
    }

    /* ── Initialise SPI bus ──────────────────────────────────────────────── */
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

    /* ── Add MFRC522 as SPI device ───────────────────────────────────────── */
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
    if (version == 0x00 || version == 0xFF) {
        ESP_LOGE(TAG, "MFRC522 not detected (version=0x%02x). Check wiring.", version);
        return PORTUNUS_ERR_DEVICE_NOT_FOUND;
    }
    ESP_LOGI(TAG, "MFRC522 detected, version=0x%02x", version);

    /* Turn antenna on */
    mfrc522_antenna_on();

    return PORTUNUS_OK;
}

portunus_err_t mfrc522_read_card(credential_t *cred)
{
    if (cred == NULL) {
        return PORTUNUS_ERR_INVALID_ARG;
    }

    memset(cred, 0, sizeof(credential_t));

    /* Step 1 — Send REQA to detect cards */
    uint8_t atqa[2];
    portunus_err_t err = picc_request(atqa);
    if (err != PORTUNUS_OK) {
        return err;  /* No card or error */
    }
    ESP_LOGD(TAG, "ATQA: 0x%02x 0x%02x", atqa[0], atqa[1]);

    /* Step 2 — Cascade level 1 anti-collision + select */
    uint8_t uid_cl1[5];  /* 4 UID bytes + BCC */
    err = picc_anticoll_select(PICC_SEL_CL1, uid_cl1);
    if (err != PORTUNUS_OK) {
        return err;
    }

    if (uid_cl1[0] == PICC_CASCADE_TAG) {
        /* 7- or 10-byte UID: first byte is cascade tag, real UID starts at [1] */
        memcpy(&cred->uid[0], &uid_cl1[1], 3);

        /* Cascade level 2 */
        uint8_t uid_cl2[5];
        err = picc_anticoll_select(PICC_SEL_CL2, uid_cl2);
        if (err != PORTUNUS_OK) {
            return err;
        }

        memcpy(&cred->uid[3], uid_cl2, 4);
        cred->uid_len = 7;
    } else {
        /* Single-size 4-byte UID */
        memcpy(cred->uid, uid_cl1, 4);
        cred->uid_len = 4;
    }

    return PORTUNUS_OK;
}

uint8_t mfrc522_get_version(void)
{
    return reg_read(REG_VERSION);
}

void mfrc522_halt_card(void)
{
    uint8_t buf[4];
    buf[0] = PICC_HLTA;
    buf[1] = 0x00;

    /* Calculate CRC_A */
    reg_write(REG_COMMAND, CMD_IDLE);
    reg_write(REG_DIV_IRQ, 0x04);
    reg_write(REG_FIFO_LEVEL, 0x80);
    reg_write(REG_FIFO_DATA, buf[0]);
    reg_write(REG_FIFO_DATA, buf[1]);
    reg_write(REG_COMMAND, CMD_CALC_CRC);

    uint16_t timeout = 5000;
    while (!(reg_read(REG_DIV_IRQ) & 0x04)) {
        if (--timeout == 0) break;
    }
    buf[2] = reg_read(REG_CRC_RESULT_L);
    buf[3] = reg_read(REG_CRC_RESULT_H);

    /* Transmit HALT — we don't expect a response */
    uint8_t recv_len = 0;
    transceive(buf, 4, NULL, &recv_len, NULL);
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