#pragma once

extern "C" {
#include "esp_err.h"
}

namespace portunus {

class EventBus;
struct Event;

class Module {
public:
  virtual ~Module() = default;

  virtual const char* name() const = 0;
  virtual esp_err_t init() = 0;
  virtual esp_err_t start(EventBus& bus) = 0;

  // Modules can react to events and/or publish new ones.
  virtual void handle(const Event& ev, EventBus& bus) { (void)ev; (void)bus; }
};

} // namespace portunus
