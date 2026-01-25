#pragma once
#include <cstdint>

extern "C" {
#include "esp_timer.h"
}

namespace portunus {

enum class EventType : uint8_t {
  Boot = 0,

  WifiConnected,
  WifiDisconnected,

  CardScanned,      // uid + arg0=uptime_ms
  DoorOpened,       // arg0=1
  DoorClosed,       // arg0=0

  AccessRequest,    // uid + arg0=uptime_ms
  AuthResult,       // arg0=1 granted / 0 denied, arg1=unlock_ms

  UnlockRequested,  // arg0=unlock_ms
  LockRequested,    // no args

  Feedback          // arg0 = FeedbackKind
};

enum class FeedbackKind : uint8_t {
  Armed = 0,
  Online,
  Offline,
  AccessGranted,
  AccessDenied,
  Error
};

// POD UID to keep event queue trivially-copyable.
struct UidBytes {
  uint8_t size = 0;
  uint8_t bytes[10] = {0};
};

struct Event {
  EventType type{EventType::Boot};
  uint64_t ts_us{0};

  uint32_t arg0{0};
  uint32_t arg1{0};
  uint32_t arg2{0};

  UidBytes uid{};

  static inline uint64_t now_us() { return (uint64_t)esp_timer_get_time(); }

  static inline Event boot() {
    Event e; e.type = EventType::Boot; e.ts_us = now_us(); return e;
  }

  static inline Event feedback(FeedbackKind k) {
    Event e; e.type = EventType::Feedback; e.ts_us = now_us(); e.arg0 = (uint32_t)k; return e;
  }

  static inline Event wifi_connected() {
    Event e; e.type = EventType::WifiConnected; e.ts_us = now_us(); return e;
  }

  static inline Event wifi_disconnected() {
    Event e; e.type = EventType::WifiDisconnected; e.ts_us = now_us(); return e;
  }

  static inline Event unlock_requested(uint32_t ms) {
    Event e; e.type = EventType::UnlockRequested; e.ts_us = now_us(); e.arg0 = ms; return e;
  }

  static inline Event lock_requested() {
    Event e; e.type = EventType::LockRequested; e.ts_us = now_us(); return e;
  }
};

} // namespace portunus
