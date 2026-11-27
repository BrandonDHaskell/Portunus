# Hardware tier: bench checklist

Checks that the IDF linux target cannot run: GPIO and SPI drivers are
unsupported there in IDF 5.5.1. Each item carries the plan ID from
`REMEDIATION_access_module_tests.md` section 6.14. A fix that depends on an
item is not closed until the item is recorded here on real hardware.

An on-target Unity app lands with chunk 5; until then these are manual.

## Before you start

### Equipment

- The ESP32-S3 access module on the bench, on USB for flashing and the
  serial monitor.
- A multimeter or logic probe on the strike output pin
  (`CONFIG_PORTUNUS_DOOR_STRIKE_PIN`). Measure at the ESP32 pin; a relay or
  MOSFET stage may be connected but is not what these checks observe.
- Cards: one 4-byte UID (MIFARE Classic) and one 7-byte UID (MIFARE
  Ultralight or NTAG). Optional: a 10-byte UID card and a second card.
- A phone NFC app that shows a card's UID (for example NXP TagInfo).

### Which build and which server

The module reads its server, module ID and HMAC secret from NVS
(`access_module/nvs_config.csv`, flashed with `task firmware:nvs:flash`), so
it talks to the server in NVS whatever the build profile.

- Use a **prod build** from the commit under test:
  `task firmware:build:prod`, then `task firmware:flash-monitor`. A dev
  build has HMAC off, and a prod server rejects its requests.
- The **strike checks (HW-01, HW-02) need a real grant**: a server running
  the same commit (the access-decision signature changed in #94, so
  firmware and server must match), with a card enrolled and authorised for
  this module's ID in the admin UI. Confirm one tap opens before starting.
- **HW-03 needs no grant**: the card read is logged before the server is
  asked, so a deny afterwards does not matter. It can run first, with any
  server.

Run them in this order: HW-03, then HW-01, then HW-02.

### Strike polarity

`CONFIG_PORTUNUS_DOOR_STRIKE_ACTIVE_LOW` (Door Configuration in
`task firmware:menuconfig`) sets the polarity. Set it, then rebuild with
`task firmware:build:prod`; profile builds keep it, along with the other
per-device wiring settings (pins, reed NC, feature switches).

**Safety:** before flashing the opposite polarity to the real wiring,
disconnect the relay or strike stage. The wrong polarity drives the strike
energized while the firmware believes it is locked. Set the polarity back to
match the wiring when done.

### Recording

Fill in the Results table at the end: date, firmware commit
(`git rev-parse --short HEAD`), and pass or fail with notes. The expected
log lines below are substrings of what the serial monitor prints.

## HW-03: card reads through the MFRC522 (F8, D-T7)

The log shows an 8-hex-digit fingerprint of each UID, never the UID, so
compute the expected fingerprint from the UID the phone app shows (hex,
no separators):

```bash
python3 -c 'import sys
h=0x811c9dc5
for b in bytes.fromhex(sys.argv[1]): h=((h^b)*0x01000193)&0xffffffff
print(f"{h:08x}")' DEADBEEF
```

1. Tap the 4-byte card. Expect exactly one `Credential read` line whose
   `id=` matches the fingerprint of its 4 UID bytes.
2. Tap the 7-byte card. Same check over all 7 bytes.
3. Optional, 10-byte card: expect no `Credential read` line and no
   `entering degraded mode`, however long it is held. A 4-byte or 7-byte
   card tapped afterwards reads normally.
4. Optional, two cards held together: expect no `Credential read` line with
   a fingerprint that matches neither card, and no `entering degraded mode`
   however long they are held.
5. Optional, power off: unplug the reader's 3.3 V (or its MISO wire) while
   idle. Expect `entering degraded mode` within about a second. Plug it back
   in; expect `Credential reader recovered` within about 5 s and card reads
   working again.

## HW-01: active-high strike (F1)

`CONFIG_PORTUNUS_DOOR_STRIKE_ACTIVE_LOW=n` (the default).

1. Boot. Expect `door_strike: Initialised on GPIO <n> (active-high)`; the
   pin reads LOW.
2. Tap the authorised card. Expect `Strike ENERGIZED (unlocked)` and
   `hold timer started`; the pin reads HIGH; the LED shows the grant.
3. Wait for the hold time (`CONFIG_PORTUNUS_UNLOCK_HOLD_MS`, 5 s by
   default). Expect `Unlock hold timer expired` then
   `Strike DE-ENERGIZED (locked)`; the pin reads LOW.
4. No `readback mismatch` (energize or de-energize) and no
   `Re-lock after timer expiry failed` lines.

## HW-02: active-low strike (F1)

`CONFIG_PORTUNUS_DOOR_STRIKE_ACTIVE_LOW=y`. Disconnect an active-high relay
or strike stage first (see Strike polarity). Before the F1 fix every lock on
this wiring failed the readback, so the FSM retried forever and never
published the unlock timeout.

1. Boot. Expect `(active-low)`; the pin reads HIGH once the driver
   initialises, and a logic probe shows no LOW pulse at that moment (the
   inactive level is latched before output enable). Before init the pin is
   in its reset state, so an active-low relay stage needs its own pull-up.
2. Tap the authorised card. The pin reads LOW, `Strike ENERGIZED (unlocked)`.
3. After the hold time: `Strike DE-ENERGIZED (locked)` exactly once; the pin
   reads HIGH.
4. No `readback mismatch` (energize or de-energize) and no repeated
   `Re-lock after timer expiry failed`.
5. If a reed switch is fitted: open and close the door during the hold.
   Expect `Door closed during unlock hold` and one
   `Strike DE-ENERGIZED (locked)`, with no timer re-lock afterwards.

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
| HW-03 | | | n/a | | |
| HW-01 | | | active-high | | |
| HW-02 | | | active-low | | |
