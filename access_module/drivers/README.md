# Portunus — Hardware Drivers

Each driver in this directory implements a Portunus interface from
`components/portunus_interfaces/`.  The directory and file naming convention
uses the **interface name as a prefix** so that drivers sort by interface:

| Directory              | Implements           | Hardware                          |
|------------------------|----------------------|-----------------------------------|
| `access_point_gpio/`   | `IAccessPoint`       | GPIO door strike + reed switch    |
| `feedback_led/`        | `IFeedback`          | Single GPIO status LED            |
| `reader_mfrc522/`      | `ICredentialReader`  | NXP MFRC522 SPI RFID reader      |

## Adding a new driver

1. **Pick the interface** your hardware fulfils (e.g. `ICredentialReader`).
2. **Create a directory** under `drivers/` prefixed with the interface name:
   `reader_pn532/`, `feedback_buzzer/`, `access_point_ble/`, etc.
3. **Copy the structure** from an existing driver of the same interface type.
4. **Implement the interface** in a class whose name matches the directory
   (e.g. `class ReaderPn532 : public ICredentialReader`).
5. All **`.h` files go in `include/`**, all **`.cpp` files go in `src/`**.
   The public interface header and internal HAL headers coexist in
   `include/` — external code uses only the interface class.
6. **Register the component** in `CMakeLists.txt` with `REQUIRES` pointing
   at `portunus_interfaces`, `portunus_config`, `portunus_types`, and any
   ESP-IDF peripheral drivers you need.
7. **Wire it up** in `main/main.cpp` — construct your driver and pass it
   to the `SystemFSM` constructor.  No other files need to change.

## Component layout

```
reader_pn532/                   ← example new driver
├── CMakeLists.txt              ← REQUIRES portunus_interfaces, driver, ...
├── include/
│   ├── reader_pn532.h          ← public: class ReaderPn532 : public ICredentialReader
│   └── pn532_hal.h             ← internal: I²C HAL declarations
└── src/
    ├── reader_pn532.cpp         ← interface method implementations
    └── pn532_hal.cpp            ← internal: I²C register-level driver
```

The build system auto-discovers new directories under `drivers/` — no
edits to the root `CMakeLists.txt` are required.
