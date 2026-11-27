// Package devicesig computes the HMAC signatures the server attaches to its
// responses to access modules. The gRPC trailer and the HTTP header carry the
// same signature, so both transports call this package rather than each
// building the projection (CLAUDE.md: two transports, one brain).
package devicesig

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// AccessResponseProjection returns the canonical string the server signs for
// an access decision and the module verifies before acting on it:
//
//	"access_resp|{module_id}|{credential_id}|{nonce_hex}|{1 or 0}"
//
// The request's nonce binds the signature to that one request, so a captured
// signed grant cannot be replayed to the module for a later read of the same
// card. The "access_resp" prefix keeps a response signature from ever matching
// a request projection ("access|...") signed with the same shared secret.
//
// Must match access_response_projection() in the firmware
// (access_module/services/server_comm/src/access_protocol.cpp); both sides
// test proto/testdata/hmac_vectors.txt (CLAUDE.md hazard #5).
func AccessResponseProjection(moduleID, credentialID string, nonce []byte, granted bool) []byte {
	v := 0
	if granted {
		v = 1
	}
	return []byte(fmt.Sprintf("access_resp|%s|%s|%s|%d",
		moduleID, credentialID, hex.EncodeToString(nonce), v))
}

// AccessResponseSig returns the hex HMAC-SHA256 of AccessResponseProjection.
// It returns "" when secret is empty (HMAC disabled) or the request carried no
// nonce, so callers attach a signature only when it is non-empty; the module
// denies any response without one.
func AccessResponseSig(secret, moduleID, credentialID string, nonce []byte, granted bool) string {
	if secret == "" || len(nonce) == 0 {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(AccessResponseProjection(moduleID, credentialID, nonce, granted))
	return hex.EncodeToString(mac.Sum(nil))
}
