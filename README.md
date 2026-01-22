# Portunus

**Local-network door access control system**: ESP32-based door modules (RFID + door strike + reed switch + etc.) communicating with a Go server (Raspberry Pi or any LAN host).

> **Project status:** Active development / early prototype
>
> **Last updated:** 2026-01-22

---

## Table of contents
- [What is Portunus?](#what-is-portunus)
- [Project goals](#project-goals)
- [Architecture](#architecture)
- [Project status](#project-status)
- [Capabilities](#capabilities)
- [Known limitations](#known-limitations)
- [Repository layout](#repository-layout)
- [Quickstart](#quickstart)
  - [Server](#server)
  - [Door module firmware](#door-module-firmware)
- [Configuration](#configuration)
- [HTTP API](#http-api)
- [Database](#database)
- [Security model](#security-model)
- [Logging and observability](#logging-and-observability)
- [Roadmap](#roadmap)
- [Contributing](#contributing)
- [License](#license)

---

## What is Portunus?
Portunus is a **LAN-first** door access system designed for maker spaces, workshops, and shared buildings.

A **door module** (ESP32) handles:
- RFID scanning (e.g., MFRC522-class reader)
- Door strike control (lock/unlock)
- Reed switch monitoring (door open/closed)
- Periodic status reporting (heartbeat)

A **server** (Go) handles:
- Receiving door-module heartbeats and access requests
- Storing audit logs and device state
- Managing access policies (who can open what, when)

---

## Project goals
- **Reliable door control** even in noisy real-world environments (power glitches, WiFi drops, reboot recovery).
- **Clear audit trail**: “who/what/when” for access decisions and door state.
- **Secure-by-design** communication over the local network (incremental hardening as the project matures).
- **Maintainable codebase**: modern C++ practices on firmware and clean Go server architecture.
- **Open and extensible**: approachable for other maker spaces to fork and adapt.

---

## Architecture

### High-level flow
1. Door module connects to WiFi.
2. Door module sends **heartbeat** to the server at a fixed interval.
3. When a card is scanned, the module sends an **access request** to the server.
4. Server responds allow/deny (and optionally duration / reason).
5. Door module actuates the strike and reports state.

### Components
- **Door module firmware (ESP-IDF / C++23)**  
  RFID, strike output, reed input, heartbeat task, HTTP client, device state machine.
- **Server (Go)**  
  HTTP API, state tracking, persistence (SQLite planned / in-progress), admin management planned.

*TODO: add system diagram here*

---

## Project status
**Server ↔ door module heartbeat is working** on a local network.

### Status log
- **2026-01-22**
  - Door module: sends heartbeat to server
  - Server: accepts heartbeat endpoint and responds
  - Firmware: WiFi manager + HTTP client integrated
  - Work in progress: persistence (SQLite schema), shared data definitions (JSON/proto)

*TODO: create separate status.md and embed here*

---

## Capabilities
### Working now
- Door module connects to WiFi and periodically reports **heartbeat**
- Server receives heartbeats and returns a response
- Basic device state reporting (uptime, RSSI, etc.)
- MFRC522 RFID reader reads RFID tags

### In progress
- Access request flow (RFID → server decision → strike control)
- SQLite storage for device events / audit trail
- Single source of truth for message formats (shared JSON schema / protobuf)

### Planned
- Admin UI and/or CLI tooling for managing users/RFID tags
- Commissioning / enrollment flow for new modules
- Stronger transport security (see [Security model](#security-model))

---

## Known limitations
- **Not production-hardened yet** (retries, offline behavior, state recovery are still evolving)
- **Security hardening is ongoing** (encryption/auth design is a roadmap item)
- Hardware wiring and pin mappings vary by setup (expects builder to configure)

---

## Repository layout
Portunus
│
├── server/                 # Go server for APIs and Admin UI
│
└── door_access_module/     # ESP-IDF folder for ESP32 fromware