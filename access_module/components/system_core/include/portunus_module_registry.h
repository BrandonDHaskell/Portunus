#pragma once
#include <cstddef>
#include "portunus_module.h"

namespace portunus {

template <size_t MaxModules>
class ModuleRegistry {
public:
  bool add(Module& m) {
    if (count_ >= MaxModules) return false;
    modules_[count_++] = &m;
    return true;
  }

  esp_err_t init_all() {
    for (size_t i = 0; i < count_; ++i) {
      esp_err_t err = modules_[i]->init();
      if (err != ESP_OK) return err;
    }
    return ESP_OK;
  }

  esp_err_t start_all(EventBus& bus) {
    for (size_t i = 0; i < count_; ++i) {
      esp_err_t err = modules_[i]->start(bus);
      if (err != ESP_OK) return err;
    }
    return ESP_OK;
  }

  void dispatch(const Event& ev, EventBus& bus) {
    for (size_t i = 0; i < count_; ++i) {
      modules_[i]->handle(ev, bus);
    }
  }

private:
  Module* modules_[MaxModules]{};
  size_t count_{0};
};

} // namespace portunus
