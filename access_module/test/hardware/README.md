# Hardware tier: bench checklist

Checks that the IDF linux target cannot run: GPIO and SPI drivers are
unsupported there in IDF 5.5.1. Each item carries the plan ID from
`REMEDIATION_access_module_tests.md` section 6.14. A fix that depends on an
item is not closed until the item is recorded here on real hardware.

An on-target Unity app lands with chunk 5; until then these are manual.

## Setup

- ESP32-S3 module flashed with an ACCESS_POINT dev build
  (`task firmware:build:dev`, then `task firmware:flash-monitor`).
- Strike output pin (`CONFIG_PORTUNUS_DOOR_STRIKE_PIN`) observed with a
  multimeter or logic probe, or looped to a spare input pin. A relay or
  MOSFET stage may be connected; the measurement is at the ESP32 pin.
- Polarity is set by `CONFIG_PORTUNUS_DOOR_STRIKE_ACTIVE_LOW` in
  `task firmware:menuconfig` (Door Configuration). Rebuild after changing it.
- The server can run with `PORTUNUS_ALLOW_ALL=true` for these items: they
  test the strike, not the credential path.

## HW-01: active-high strike (F1)

`CONFIG_PORTUNUS_DOOR_STRIKE_ACTIVE_LOW=n`.

1. Boot. Expect `door_strike: Initialised on GPIO <n> (active-high)`; pin reads LOW.
2. Tap a card. Expect `Strike ENERGIZED (unlocked)` and
   `hold timer started`; pin reads HIGH.
3. Wait for the hold time (`CONFIG_PORTUNUS_UNLOCK_HOLD_MS`). Expect
   `Unlock hold timer expired` then `Strike DE-ENERGIZED (locked)`;
   pin reads LOW.
4. No `readback mismatch` (energize or de-energize) and no
   `Re-lock after timer expiry failed` lines.

## HW-02: active-low strike (F1)

`CONFIG_PORTUNUS_DOOR_STRIKE_ACTIVE_LOW=y`. Before the F1 fix every lock on
this wiring failed the readback, so the FSM retried forever and never
published the unlock timeout.

1. Boot. Expect `(active-low)`; pin reads HIGH once the driver initialises,
   and a logic probe shows no LOW pulse at that moment (the inactive level
   is latched before output enable). Before init the pin is in its reset
   state, so an active-low relay stage needs its own pull-up.
2. Tap a card. Pin reads LOW, `Strike ENERGIZED (unlocked)`.
3. After the hold time: `Strike DE-ENERGIZED (locked)` exactly once; pin
   reads HIGH.
4. No `readback mismatch` (energize or de-energize) and no repeated
   `Re-lock after timer expiry failed`.
5. If a reed switch is fitted: open and close the door during the hold.
   Expect `Door closed during unlock hold` and one
   `Strike DE-ENERGIZED (locked)`, with no timer re-lock afterwards.

## HW-03: card reads through the MFRC522 (F8, D-T7)

Any ACCESS_POINT build. The log shows an 8-hex-digit fingerprint of the
UID, never the UID, so compare against the fingerprint of the UID printed
on the card or read with a phone NFC app:

```bash
python3 -c 'import sys
h=0x811c9dc5
for b in bytes.fromhex(sys.argv[1]): h=((h^b)*0x01000193)&0xffffffff
print(f"{h:08x}")' DEADBEEF
```

1. Tap a 4-byte card (MIFARE Classic). Expect one `Credential read` line
   whose `id=` matches the fingerprint of its 4 UID bytes.
2. Tap a 7-byte card (MIFARE Ultralight or NTAG). Same check over all
   7 bytes.
3. Optional, 10-byte card: expect no `Credential read` line and no
   `entering degraded mode`, however long it is held. A 4-byte or 7-byte
   card tapped afterwards reads normally.
4. Optional, two cards held together: expect no `Credential read` line
   with a fingerprint that matches neither card, and no
   `entering degraded mode` however long they are held.
5. Optional, power off: unplug the reader's 3.3 V (or its MISO wire) while
   idle. Expect `entering degraded mode` within about a second. Plug it
   back in; expect `Credential reader recovered` within about 5 s and
   card reads working again.

## Deferred to the on-target tier

Not manual checks: listed so the chunk 5 on-target app covers them. The host
tiers cannot run them because the linux target has no `esp_wifi`.

- **WIFI-HW** (`wifi_mgr` lifecycle). After `wifi_mgr_stop()`, no
  `Reconnect: connecting` line follows `WiFi manager stopped`, and a later
  `wifi_mgr_start()` connects again. `wifi_mgr_deinit()` returns OK, and
  `wifi_mgr_init()` then succeeds a second time. The component tier (`wifi`
  variant) tests only the reconnect task, with fake callbacks, not the
  driver calls or the unregistering of event handlers.

## Results

| ID | Date | Firmware commit | Polarity | Result | Notes |
|---|---|---|---|---|---|
| HW-01 | | | active-high | | |
| HW-02 | | | active-low | | |
| HW-03 | | | n/a | | |
