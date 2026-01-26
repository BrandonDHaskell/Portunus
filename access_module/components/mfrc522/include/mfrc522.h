#pragma once

#include <array>
#include <cstdint>

extern "C" {
#include "driver/gpio.h"
#include "driver/spi_master.h"
#include "esp_err.h"
}

namespace portunus {

struct Mfrc522Pins {
  gpio_num_t cs;
  gpio_num_t sck;
  gpio_num_t mosi;
  gpio_num_t miso;
  gpio_num_t rst; // active-low reset pin on most RC522 breakouts
};

struct RfidUid {
  uint8_t size = 0;                       // 4, 7, or 10
  std::array<uint8_t, 10> bytes{};        // padded with zeros
};

class Mfrc522 {
public:
  Mfrc522() = default;
  ~Mfrc522();

  Mfrc522(const Mfrc522&) = delete;
  Mfrc522& operator=(const Mfrc522&) = delete;

  esp_err_t init(spi_host_device_t host, const Mfrc522Pins& pins, int clock_hz = 2'000'000);
  esp_err_t deinit();

  // Returns true if a UID was read successfully.
  bool read_uid(RfidUid& out_uid);

  // Optional: version register (0x91/0x92 commonly)
  uint8_t version() { return read_reg(0x37); }

private:
  // ---- Low-level register I/O ----
  uint8_t read_reg(uint8_t reg);
  void    read_regs(uint8_t reg, uint8_t* out, size_t len);
  void    write_reg(uint8_t reg, uint8_t value);
  void    write_regs(uint8_t reg, const uint8_t* data, size_t len);
  void    set_bitmask(uint8_t reg, uint8_t mask);
  void    clear_bitmask(uint8_t reg, uint8_t mask);

  // ---- Chip functions ----
  void hard_reset_pin();
  void soft_reset();
  void antenna_on();

  // Transceive data to/from PICC (card). Returns true on RX success.
  bool transceive(const uint8_t* tx, size_t tx_len,
                  uint8_t* rx, size_t& rx_len,
                  uint8_t tx_last_bits /*0..7*/, uint32_t timeout_ms);

  bool request_a();                  // REQA
  bool anticollision_cl1(uint8_t uid_cl1[5]); // returns 4 UID bytes + BCC (5 bytes)

  bool calculate_crc(const uint8_t* data, size_t len, uint8_t out_crc[2]);

private:
  spi_host_device_t host_ = SPI2_HOST;
  spi_device_handle_t dev_ = nullptr;
  Mfrc522Pins pins_{};
  bool initialized_ = false;
};

} // namespace portunus
