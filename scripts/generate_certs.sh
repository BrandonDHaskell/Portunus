#!/usr/bin/env bash
# ──────────────────────────────────────────────────────────────────────────────
# generate_certs.sh — Generate a private CA + server certificate for Portunus
#
# Creates a minimal internal Certificate Authority and uses it to sign a
# server certificate that the ESP32 firmware can validate.  The CA cert
# is also copied into the firmware source tree so it can be embedded in
# the flash image at build time.
#
# Usage:
#   ./scripts/generate_certs.sh                       # interactive prompts
#   ./scripts/generate_certs.sh --ip 192.168.1.100    # non-interactive
#   ./scripts/generate_certs.sh --ip 192.168.1.100 --dns portunus.local
#   ./scripts/generate_certs.sh --ip 192.168.1.100 --days 730
#
# Output (all files written to <repo>/certs/):
#   ca.key          — CA private key           (KEEP SECRET)
#   ca.pem          — CA certificate            → embed in ESP32 firmware
#   server.key      — Server private key        (KEEP SECRET)
#   server.pem      — Server certificate        → PORTUNUS_TLS_CERT_FILE
#   server.csr      — Server CSR (intermediate, can be deleted)
#
# The script also copies ca.pem into the firmware tree at:
#   access_module/certs/ca_cert.pem
# so that CMakeLists.txt can embed it via EMBED_TXTFILES.
#
# Requires: openssl >= 1.1.1
# ──────────────────────────────────────────────────────────────────────────────
set -euo pipefail

# ── Defaults ──────────────────────────────────────────────────────────────────
CA_DAYS=3650          # CA valid for 10 years (long-lived, rotated manually)
SERVER_DAYS=825       # Server cert valid ~2.25 years (Apple max)
KEY_BITS=2048         # RSA key size
SERVER_IP=""
SERVER_DNS=""
EXTRA_DAYS=""

# ── Resolve repo root ────────────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
OUT_DIR="$REPO_ROOT/certs"
FW_CERT_DIR="$REPO_ROOT/access_module/certs"

# ── Parse arguments ──────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
    case "$1" in
        --ip)    SERVER_IP="$2";    shift 2 ;;
        --dns)   SERVER_DNS="$2";   shift 2 ;;
        --days)  EXTRA_DAYS="$2";   shift 2 ;;
        --help|-h)
            head -30 "$0" | grep '^#' | sed 's/^# \?//'
            exit 0
            ;;
        *)
            echo "Unknown option: $1 (try --help)" >&2
            exit 1
            ;;
    esac
done

if [[ -n "$EXTRA_DAYS" ]]; then
    SERVER_DAYS="$EXTRA_DAYS"
fi

# ── Prompt for IP if not provided ────────────────────────────────────────────
if [[ -z "$SERVER_IP" ]]; then
    # Try to detect the default LAN IP for convenience
    DEFAULT_IP=""
    if command -v ip &>/dev/null; then
        DEFAULT_IP=$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src") print $(i+1)}' || true)
    elif command -v ifconfig &>/dev/null; then
        DEFAULT_IP=$(ifconfig 2>/dev/null | awk '/inet / && !/127\.0\.0\.1/ {print $2; exit}' || true)
    fi

    if [[ -n "$DEFAULT_IP" ]]; then
        read -rp "Server LAN IP address [$DEFAULT_IP]: " SERVER_IP
        SERVER_IP="${SERVER_IP:-$DEFAULT_IP}"
    else
        read -rp "Server LAN IP address: " SERVER_IP
    fi
fi

if [[ -z "$SERVER_IP" ]]; then
    echo "Error: server IP is required." >&2
    exit 1
fi

# ── Validate IP format (basic check) ────────────────────────────────────────
if ! [[ "$SERVER_IP" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "Error: '$SERVER_IP' doesn't look like an IPv4 address." >&2
    exit 1
fi

# ── Prepare output directories ───────────────────────────────────────────────
mkdir -p "$OUT_DIR" "$FW_CERT_DIR"

echo ""
echo "╔══════════════════════════════════════════════════╗"
echo "║  Portunus TLS Certificate Generator              ║"
echo "╠══════════════════════════════════════════════════╣"
echo "║  Server IP:    $SERVER_IP"
if [[ -n "$SERVER_DNS" ]]; then
echo "║  Server DNS:   $SERVER_DNS"
fi
echo "║  CA validity:  $CA_DAYS days"
echo "║  Cert validity: $SERVER_DAYS days"
echo "║  Output:       $OUT_DIR/"
echo "╚══════════════════════════════════════════════════╝"
echo ""

# ── Build SAN extension ─────────────────────────────────────────────────────
# The Subject Alternative Name (SAN) is what modern TLS clients actually
# check.  CN alone is deprecated.  We always include the IP; optionally
# a DNS name too (useful if you set up mDNS / local DNS).
SAN="IP:${SERVER_IP}"
if [[ -n "$SERVER_DNS" ]]; then
    SAN="${SAN},DNS:${SERVER_DNS}"
fi

# Write a temporary openssl config for the server cert extensions.
# This is needed because openssl's -addext flag doesn't support all
# the options we need in older versions.
SERVER_EXT_CNF=$(mktemp)
trap 'rm -f "$SERVER_EXT_CNF"' EXIT

cat > "$SERVER_EXT_CNF" <<EOF
[v3_server]
basicConstraints       = CA:FALSE
keyUsage               = digitalSignature, keyEncipherment
extendedKeyUsage       = serverAuth
subjectAltName         = ${SAN}
subjectKeyIdentifier   = hash
authorityKeyIdentifier = keyid,issuer
EOF

# ══════════════════════════════════════════════════════════════════════════════
#  Step 1: Certificate Authority
# ══════════════════════════════════════════════════════════════════════════════
echo "→ Generating CA private key..."
openssl genrsa -out "$OUT_DIR/ca.key" "$KEY_BITS" 2>/dev/null

echo "→ Generating CA certificate (self-signed, ${CA_DAYS}d)..."
openssl req -new -x509 \
    -key "$OUT_DIR/ca.key" \
    -out "$OUT_DIR/ca.pem" \
    -days "$CA_DAYS" \
    -subj "/C=US/O=Portunus/CN=Portunus CA" \
    -addext "basicConstraints=critical,CA:TRUE,pathlen:0" \
    -addext "keyUsage=critical,keyCertSign,cRLSign" \
    2>/dev/null

# ══════════════════════════════════════════════════════════════════════════════
#  Step 2: Server certificate
# ══════════════════════════════════════════════════════════════════════════════
echo "→ Generating server private key..."
openssl genrsa -out "$OUT_DIR/server.key" "$KEY_BITS" 2>/dev/null

echo "→ Generating server CSR..."
openssl req -new \
    -key "$OUT_DIR/server.key" \
    -out "$OUT_DIR/server.csr" \
    -subj "/C=US/O=Portunus/CN=${SERVER_IP}" \
    2>/dev/null

echo "→ Signing server certificate with CA (${SERVER_DAYS}d)..."
openssl x509 -req \
    -in "$OUT_DIR/server.csr" \
    -CA "$OUT_DIR/ca.pem" \
    -CAkey "$OUT_DIR/ca.key" \
    -CAcreateserial \
    -out "$OUT_DIR/server.pem" \
    -days "$SERVER_DAYS" \
    -extfile "$SERVER_EXT_CNF" \
    -extensions v3_server \
    2>/dev/null

# ══════════════════════════════════════════════════════════════════════════════
#  Step 3: Copy CA cert into firmware tree for embedding
# ══════════════════════════════════════════════════════════════════════════════
cp "$OUT_DIR/ca.pem" "$FW_CERT_DIR/ca_cert.pem"
echo "→ Copied CA cert to $FW_CERT_DIR/ca_cert.pem"

# ══════════════════════════════════════════════════════════════════════════════
#  Step 4: Set file permissions
# ══════════════════════════════════════════════════════════════════════════════
chmod 600 "$OUT_DIR/ca.key" "$OUT_DIR/server.key"
chmod 644 "$OUT_DIR/ca.pem" "$OUT_DIR/server.pem" "$FW_CERT_DIR/ca_cert.pem"

# ══════════════════════════════════════════════════════════════════════════════
#  Step 5: Verify
# ══════════════════════════════════════════════════════════════════════════════
echo ""
echo "── Verification ──────────────────────────────────────────"
echo ""
echo "CA certificate:"
openssl x509 -in "$OUT_DIR/ca.pem" -noout -subject -dates | sed 's/^/  /'
echo ""
echo "Server certificate:"
openssl x509 -in "$OUT_DIR/server.pem" -noout -subject -dates | sed 's/^/  /'
echo "  SAN:"
openssl x509 -in "$OUT_DIR/server.pem" -noout -ext subjectAltName 2>/dev/null | grep -v "Subject Alternative" | sed 's/^/    /'
echo ""

echo "Chain verification:"
if openssl verify -CAfile "$OUT_DIR/ca.pem" "$OUT_DIR/server.pem" 2>/dev/null | grep -q ": OK"; then
    echo "  ✓ server.pem validates against ca.pem"
else
    echo "  ✗ CHAIN VERIFICATION FAILED" >&2
    exit 1
fi

# ══════════════════════════════════════════════════════════════════════════════
#  Summary
# ══════════════════════════════════════════════════════════════════════════════
echo ""
echo "── Next steps ────────────────────────────────────────────"
echo ""
echo "  1. Start the server with TLS:"
echo ""
echo "     export PORTUNUS_TLS_CERT_FILE=$OUT_DIR/server.pem"
echo "     export PORTUNUS_TLS_KEY_FILE=$OUT_DIR/server.key"
echo "     export PORTUNUS_HTTP_ADDR=:8443"
echo ""
echo "  2. Verify with curl:"
echo ""
echo "     curl --cacert $OUT_DIR/ca.pem https://${SERVER_IP}:8443/v1/heartbeat"
echo ""
echo "  3. Build the firmware (CA cert is already in the source tree):"
echo ""
echo "     cd access_module && idf.py build"
echo ""
echo "  4. Ensure these Kconfig options are set:"
echo ""
echo "     CONFIG_PORTUNUS_USE_TLS=y"
echo "     CONFIG_PORTUNUS_TLS_SERVER_PORT=8443"
echo "     CONFIG_PORTUNUS_TLS_SKIP_VERIFY=n     ← no longer needed!"
echo ""