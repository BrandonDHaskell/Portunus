# Hardware tier

Checks that the IDF linux target cannot run, because GPIO, SPI and the pulse
counter do not exist there. An on-target Unity app (`main/test_hw_main.cpp`)
runs them on a bench ESP32-S3 against the real drivers. IDs are from
`REMEDIATION_access_module_tests.md` section 6.14. The manual checklist that
used to be here numbered the card-read check HW-03; it is now HW-04 and
HW-05, as in the plan.

A fix that depends on an item is closed once a run of the item is recorded
under Results.

## Bench setup

### Wiring

The production pins (Kconfig defaults and the saved device wiring), plus two
loopback jumpers so the board can measure its own pins:

| Signal | ESP32-S3 pin | Connected to |
|---|---|---|
| Strike output | GPIO 5 | relay input, **and jumper to GPIO 15** |
| Strike sense (test only) | GPIO 15 | jumper from GPIO 5 |
| Reed switch input | GPIO 6 | reed switch to GND, **and jumper from GPIO 18** |
| Reed drive (test only, open-drain) | GPIO 18 | jumper to GPIO 6 |
| MFRC522 SPI | MOSI 37, MISO 38, SCLK 36, CS 35 | reader |
| MFRC522 reset | GPIO 4 | reader RST |

Change the test pins in `main/Kconfig.projbuild` (`PORTUNUS_HW_SENSE_PIN`,
`PORTUNUS_HW_REED_DRIVE_PIN`) if 15 or 18 are taken.

### Before each run

- **No door on the relay.** The tests switch the strike output on and off
  about 70 times per variant, and build B runs it active-low. Use a bench
  relay or nothing.
- **Reed switch open**: keep the magnet away. The drive pin only pulls low,
  so it cannot short a closed switch, but it also cannot make the "open"
  level while the switch holds the pin low. The reed tests fail with a
  message saying so.
- Optional: leave one card resting on the reader for HW-04's card check. It
  is skipped (IGNORE) otherwise. Use a 4-byte card on one run and a 7-byte
  card on another to cover both UID lengths (F8).

The app replaces the board's firmware. It uses the firmware's partition
table and never touches the `nvs` partition, so the board's device config
(WiFi, server, HMAC secret) is kept. Reflash the firmware with
`task firmware:build:prod` and `task firmware:flash` afterwards.

## Running

```bash
. ~/esp/esp-idf/export.sh
task test:firmware:hardware PORT=/dev/ttyACM0                     # both variants
task test:firmware:hardware PORT=/dev/ttyACM0 VARIANT=active_low  # one
```

`scripts/hw_test.py` builds each variant, flashes it, waits for the app's
`HW_TESTS_READY` line, sends a go byte, and reads until `HW_TESTS_DONE`.
Unity's summary decides the exit code. Full serial logs, with build and
flash output, are in `build/logs/`.

| Variant | Strike | Reed |
|---|---|---|
| `active_high` | active-high | normally open |
| `active_low` | active-low | normally closed |

CI compiles both variants but cannot run them (no board).

## Tests

| ID | Test | Checks |
|---|---|---|
| HW-01/02 | `test_strike_init_has_no_glitch` | init leaves the strike locked with no pulse: the pulse counter sees at most one edge on the sense pin (F1) |
| HW-01/02 | `test_strike_unlock_lock_at_pin` | `unlock()` and `lock()` return OK and the sense pin follows, with exactly two edges per cycle; on active-low wiring every `lock()` used to fail (F1) |
| HW-03 | `test_reed_debounced_transitions` | open and close are reported correctly for this variant's NO/NC wiring, after the debounce |
| HW-03 | `test_reed_ignores_short_bounce` | a pulse shorter than the debounce window changes nothing |
| HW-04 | `test_reader_detected` | the MFRC522 answers on SPI (VersionReg) |
| HW-05 | `test_reader_power_down_and_recover` | with reset held low (standing in for an unplugged reader) reads report the chip missing within 5 polls; `init()` brings it back (D-T7) |
| HW-04 | `test_resting_card_read_once` | a resting card reads with 4 or 7 UID bytes and, once halted, is not read again (F8); IGNORE without a card |
| HW-10 | `test_heap_flat_across_cycles` | free heap does not drop across 1,000 reader polls and 50 strike cycles |

The app prints a card's UID length, never the UID.

What this tier does not cover: the FSM's grant, hold and re-lock sequence
(the component tier covers it against fakes), 10-byte and two-card
collisions, and anything that needs the network.

## Deferred to phase 5b

Not yet automated:

- **HW-06 / WIFI-HW**: AP power cycle and reconnect backoff;
  `wifi_mgr_stop()` makes no reconnect attempt and a later
  `wifi_mgr_start()` connects; `wifi_mgr_deinit()` then `wifi_mgr_init()`
  works. The component tier (`wifi` variant) tests only the reconnect task.
- **HW-07**: TLS with the pinned CA; a wrong CA fails and backs off.
- **HW-08**: end-to-end grant and deny against a real server with HMAC on.
- **HW-09**: stack high-water marks for the full firmware under load.

## Results

| ID | Date | Firmware commit | Variant | Result | Notes |
|---|---|---|---|---|---|
| HW-01/02, 03, 04, 05, 10 | | | active_high | | |
| HW-01/02, 03, 04, 05, 10 | | | active_low | | |
