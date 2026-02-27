/**
 * @file system_states.h
 * @brief System-level state definitions for the Portunus FSM.
 *
 * The full FSM (INIT → OPERATIONAL → ERROR with sub-states) is a Phase 2
 * deliverable. For the MVP only the top-level states are defined so that
 * main.cpp can track basic initialisation progress.
 */

#pragma once

#ifdef __cplusplus
extern "C" {
#endif

/**
 * @brief Top-level system states.
 */
typedef enum {
    SYSTEM_STATE_BOOT,           /**< Power-on, pre-initialisation */
    SYSTEM_STATE_INITIALIZING,   /**< Subsystems starting up */
    SYSTEM_STATE_CONNECTING,     /**< WiFi association / waiting for IP */
    SYSTEM_STATE_OPERATIONAL,    /**< All MVP subsystems running normally */
    SYSTEM_STATE_ERROR,          /**< Unrecoverable error; requires restart */
} system_state_t;

/**
 * @brief Runtime capability flags set during FSM initialisation.
 *
 * Each flag indicates whether the corresponding hardware module
 * initialised successfully.  The FSM adapts its behaviour based
 * on which capabilities are available.
 */
typedef struct {
    bool has_reader;       /**< ICredentialReader initialised successfully */
    bool has_access_point; /**< IAccessPoint initialised successfully */
    bool has_feedback;     /**< IFeedback initialised successfully */
    bool has_network;      /**< WiFi connected (updated dynamically) */
} system_capabilities_t;

#ifdef __cplusplus
}
#endif