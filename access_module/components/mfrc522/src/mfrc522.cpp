#include "mfrc522.h"

#include <cstring>

extern "C" {
#include "esp_log.h"
#include "esp_timer.h"
#include "esp_check.h"
#include "esp_rom_sys.h"
}

namespace portunus {

static const char* TAG = "mfrc522";

// Registers (subset)
static constexpr uint8_t CommandReg     = 0x01;
static constexpr uint8_t ComIrqReg      = 0x04;
static constexpr uint8_t DivIrqReg      = 0x05;
static constexpr uint8_t ErrorReg       = 0x06;
static constexpr uint8_t Status1Reg     = 0x07;
static constexpr uint8_t FIFODataReg    = 0x09;
static constexpr uint8_t FIFOLevelReg   = 0x0A;
static constexpr uint8_t ControlReg     = 0x0C;
static constexpr uint8_t BitFramingReg  = 0x0D;

static constexpr uint8_t ModeReg        = 0x11;
static constexpr uint8_t TxASKReg       = 0x15;
static constexpr uint8_t TxControlReg   = 0x14;

static constexpr uint8_t TModeReg       = 0x2A;
static constexpr uint8_t TPrescalerReg  = 0x2B;
static constexpr uint8_t TReloadRegH    = 0x2C;
static constexpr uint8_t TReloadRegL    = 0x2D;

static constexpr uint8_t CRCResultRegH  = 0x21;
static constexpr uint8_t CRCResultRegL  = 0x22;
static constexpr uint8_t RFCfgReg       = 0x26;

// Command codes (CommandReg Command[3:0])
static constexpr uint8_t Cmd_Idle       = 0x00;
static constexpr uint8_t Cmd_CalcCRC    = 0x03;
static constexpr uint8_t Cmd_Transceive = 0x0C;
static constexpr uint8_t Cmd_SoftReset  = 0x0F;

// IRQ masks (ComIrqReg)
static constexpr uint8_t Irq_Rx   = (1u << 5);
static constexpr uint8_t Irq_Idle = (1u << 4);
static constexpr uint8_t Irq_Err  = (1u << 1);
static constexpr uint8_t Irq_Tmr  = (1u << 0);

// ErrorReg masks
static constexpr uint8_t Err_BufferOvfl = (1u << 4);
static constexpr uint8_t Err_Parity     = (1u << 1);
static constexpr uint8_t Err_Protocol   = (1u << 0);
static constexpr uint8_t Err_CRC        = (1u << 2);

// BitFramingReg
static constexpr uint8_t StartSend = (1u << 7);

// FIFOLevelReg
static constexpr uint8_t FlushBuffer = (1u << 7);

// TxControlReg
static constexpr uint8_t TxControl_AntennaOn = 0x03; // bits 1:0

Mfrc522::~Mfrc522() { (void)deinit(); }

esp_err_t Mfrc522::init(spi_host_device_t host, const Mfrc522Pins& pins, int clock_hz) {
  host_ = host;
  pins_ = pins;

  // RST pin
  gpio_config_t rst_cfg{};
  rst_cfg.pin_bit_mask = 1ULL << static_cast<uint32_t>(pins_.rst);
  rst_cfg.mode = GPIO_MODE_OUTPUT;
  rst_cfg.pull_up_en = GPIO_PULLUP_DISABLE;
  rst_cfg.pull_down_en = GPIO_PULLDOWN_DISABLE;
  rst_cfg.intr_type = GPIO_INTR_DISABLE;
  ESP_RETURN_ON_ERROR(gpio_config(&rst_cfg), TAG, "gpio_config(rst)");

  // SPI bus
  spi_bus_config_t buscfg{};
  buscfg.mosi_io_num = pins_.mosi;
  buscfg.miso_io_num = pins_.miso;
  buscfg.sclk_io_num = pins_.sck;
  buscfg.quadwp_io_num = -1;
  buscfg.quadhd_io_num = -1;

  esp_err_t err = spi_bus_initialize(host_, &buscfg, SPI_DMA_CH_AUTO);
  if (err != ESP_OK && err != ESP_ERR_INVALID_STATE) {
    ESP_LOGE(TAG, "spi_bus_initialize failed: %s", esp_err_to_name(err));
    return err;
  }

  spi_device_interface_config_t devcfg{};
  devcfg.clock_speed_hz = clock_hz;
  devcfg.mode = 0; // SPI mode 0
  devcfg.spics_io_num = pins_.cs;
  devcfg.queue_size = 1;

  ESP_RETURN_ON_ERROR(spi_bus_add_device(host_, &devcfg, &dev_), TAG, "spi_bus_add_device");

  hard_reset_pin();
  soft_reset();

  // Typical ISO14443A-friendly defaults used widely with RC522:
  write_reg(TModeReg, 0x8D);        // TAuto=1, higher bits per common RC522 init
  write_reg(TPrescalerReg, 0x3E);
  write_reg(TReloadRegH, 0x00);
  write_reg(TReloadRegL, 30);
  write_reg(TxASKReg, 0x40);        // 100% ASK
  write_reg(ModeReg, 0x3D);         // CRC preset 0x6363 (common)
  write_reg(RFCfgReg, 0x70);        // RxGain = 48 dB (max) :contentReference[oaicite:1]{index=1}

  antenna_on();

  initialized_ = true;
  ESP_LOGI(TAG, "MFRC522 init OK, VersionReg=0x%02X", version());
  return ESP_OK;
}

esp_err_t Mfrc522::deinit() {
  if (!initialized_) return ESP_OK;
  initialized_ = false;

  if (dev_) {
    spi_bus_remove_device(dev_);
    dev_ = nullptr;
  }
  // Only free bus if this component owns it. If shared, remove this call.
  // spi_bus_free(host_);

  return ESP_OK;
}

void Mfrc522::hard_reset_pin() {
  gpio_set_level(pins_.rst, 0);
  esp_rom_delay_us(5'000);
  gpio_set_level(pins_.rst, 1);
  esp_rom_delay_us(5'000);
}

void Mfrc522::soft_reset() {
  write_reg(CommandReg, Cmd_SoftReset);
  esp_rom_delay_us(50'000);
}

void Mfrc522::antenna_on() {
  uint8_t v = read_reg(TxControlReg);
  if ((v & TxControl_AntennaOn) != TxControl_AntennaOn) {
    set_bitmask(TxControlReg, TxControl_AntennaOn);
  }
}

uint8_t Mfrc522::read_reg(uint8_t reg) {
  uint8_t out = 0;
  read_regs(reg, &out, 1);
  return out;
}

void Mfrc522::read_regs(uint8_t reg, uint8_t* out, size_t len) {
  // SPI address format: MSB=1 for read, bits 6..1=addr, LSB=0 :contentReference[oaicite:2]{index=2}
  const uint8_t addr = static_cast<uint8_t>(((reg << 1) & 0x7E) | 0x80);

  uint8_t tx[1 + 64] = {0};
  uint8_t rx[1 + 64] = {0};
  tx[0] = addr;

  spi_transaction_t t{};
  t.length = 8 * (1 + len);
  t.tx_buffer = tx;
  t.rx_buffer = rx;

  (void)spi_device_transmit(dev_, &t);

  std::memcpy(out, &rx[1], len);
}

void Mfrc522::write_reg(uint8_t reg, uint8_t value) {
  write_regs(reg, &value, 1);
}

void Mfrc522::write_regs(uint8_t reg, const uint8_t* data, size_t len) {
  // SPI write: MSB=0, bits 6..1=addr, LSB=0 :contentReference[oaicite:3]{index=3}
  const uint8_t addr = static_cast<uint8_t>((reg << 1) & 0x7E);

  uint8_t tx[1 + 64] = {0};
  tx[0] = addr;
  std::memcpy(&tx[1], data, len);

  spi_transaction_t t{};
  t.length = 8 * (1 + len);
  t.tx_buffer = tx;

  (void)spi_device_transmit(dev_, &t);
}

void Mfrc522::set_bitmask(uint8_t reg, uint8_t mask) {
  write_reg(reg, read_reg(reg) | mask);
}

void Mfrc522::clear_bitmask(uint8_t reg, uint8_t mask) {
  write_reg(reg, read_reg(reg) & static_cast<uint8_t>(~mask));
}

bool Mfrc522::calculate_crc(const uint8_t* data, size_t len, uint8_t out_crc[2]) {
  write_reg(CommandReg, Cmd_Idle);
  write_reg(DivIrqReg, 0x04);            // clear CRCIRq by writing bit pattern commonly used
  write_reg(FIFOLevelReg, FlushBuffer);  // flush FIFO :contentReference[oaicite:4]{index=4}

  write_regs(FIFODataReg, data, len);
  write_reg(CommandReg, Cmd_CalcCRC);

  const int64_t start_us = esp_timer_get_time();
  while (true) {
    uint8_t n = read_reg(Status1Reg);
    if (n & (1u << 5)) break; // CRCReady bit
    if ((esp_timer_get_time() - start_us) > 20'000) return false;
  }

  out_crc[0] = read_reg(CRCResultRegL);
  out_crc[1] = read_reg(CRCResultRegH);
  write_reg(CommandReg, Cmd_Idle);
  return true;
}

bool Mfrc522::transceive(const uint8_t* tx, size_t tx_len,
                        uint8_t* rx, size_t& rx_len,
                        uint8_t tx_last_bits, uint32_t timeout_ms) {
  const size_t cap = rx_len;
  rx_len = 0;

  write_reg(CommandReg, Cmd_Idle);
  write_reg(ComIrqReg, 0x7F);            // clear all IRQ request bits :contentReference[oaicite:5]{index=5}
  write_reg(FIFOLevelReg, FlushBuffer);  // flush FIFO :contentReference[oaicite:6]{index=6}

  write_regs(FIFODataReg, tx, tx_len);

  // TxLastBits in bits 2..0; StartSend in bit 7
  write_reg(BitFramingReg, tx_last_bits & 0x07);
  write_reg(CommandReg, Cmd_Transceive);
  set_bitmask(BitFramingReg, StartSend);

  const int64_t start_us = esp_timer_get_time();
  while (true) {
    const uint8_t irq = read_reg(ComIrqReg);

    if (irq & Irq_Rx) break;
    if (irq & Irq_Err) break;
    if (irq & Irq_Tmr) return false;

    if ((esp_timer_get_time() - start_us) > static_cast<int64_t>(timeout_ms) * 1000) {
      return false;
    }
  }

  clear_bitmask(BitFramingReg, StartSend);

  const uint8_t err = read_reg(ErrorReg);
  if (err & (Err_BufferOvfl | Err_Parity | Err_Protocol | Err_CRC)) {
    return false;
  }

  uint8_t fifo_level = read_reg(FIFOLevelReg) & 0x7F;
  if (fifo_level == 0) return false;

  if (fifo_level > cap) fifo_level = static_cast<uint8_t>(cap);
  for (uint8_t i = 0; i < fifo_level; i++) rx[i] = read_reg(FIFODataReg);
  rx_len = fifo_level;
  return true;

  if (fifo_level > rx_len) {
    // caller provides max in rx_len
    fifo_level = static_cast<uint8_t>(rx_len);
  }

  for (uint8_t i = 0; i < fifo_level; i++) {
    rx[i] = read_reg(FIFODataReg);
  }
  rx_len = fifo_level;

  return true;
}

bool Mfrc522::request_a() {
  // REQA is a 7-bit frame (0x26)
  const uint8_t req = 0x26;
  uint8_t atqa[2] = {0};
  size_t atqa_len = sizeof(atqa);

  // tx_last_bits=7 means last byte has only 7 valid bits (REQA) :contentReference[oaicite:7]{index=7}
  return transceive(&req, 1, atqa, atqa_len, 7, 50) && atqa_len == 2;
}

bool Mfrc522::anticollision_cl1(uint8_t uid_cl1[5]) {
  // Anticollision CL1: 0x93 0x20
  const uint8_t cmd[2] = {0x93, 0x20};
  size_t rx_len = 5;

  if (!transceive(cmd, 2, uid_cl1, rx_len, 0, 50) || rx_len != 5) return false;

  const uint8_t bcc = uid_cl1[0] ^ uid_cl1[1] ^ uid_cl1[2] ^ uid_cl1[3];
  return (bcc == uid_cl1[4]);
}

bool Mfrc522::read_uid(RfidUid& out_uid) {
  out_uid = {};

  if (!initialized_) return false;
  if (!request_a()) return false;

  uint8_t cl1[5] = {0};
  if (!anticollision_cl1(cl1)) return false;

  // v0: support the common 4-byte UID case. If cascade tag (0x88), you can extend later.
  if (cl1[0] == 0x88) {
    // Cascade Tag indicates UID is longer than 4 bytes (7/10). Not implemented in this minimal v0.
    return false;
  }

  out_uid.size = 4;
  for (int i = 0; i < 4; i++) out_uid.bytes[i] = cl1[i];
  return true;
}

} // namespace portunus