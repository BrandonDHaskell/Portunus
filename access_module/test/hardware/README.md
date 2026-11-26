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
4. No `readback mismatch` and no `Re-lock after timer expiry failed` lines.

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
4. No `readback mismatch` and no repeated `Re-lock after timer expiry failed`.
5. If a reed switch is fitted: open and close the door during the hold.
   Expect `Door closed during unlock hold` and one
   `Strike DE-ENERGIZED (locked)`, with no timer re-lock afterwards.

## Results

| ID | Date | Firmware commit | Polarity | Result | Notes |
|---|---|---|---|---|---|
| HW-01 | | | active-high | | |
| HW-02 | | | active-low | | |
