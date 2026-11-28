#!/usr/bin/env bash
# ──────────────────────────────────────────────────────────────────────────────
# generate_certs.sh: private CA + server certificate for Portunus
#
# Every flashed module pins the CA (access_module/certs/ca_cert.pem), so a new
# CA means reflashing every module. This script therefore never replaces an
# existing CA unless told to:
#
#   (no CA yet)    first run: creates the CA and a server cert
#   --sign-only    signs a new server cert with the existing CA (new IP, new
#                  DNS name, renewal). Modules keep working.
#   --new-ca       rotates the CA. The old certs/ directory is moved to
#                  certs/backup-<timestamp>/ first. Reflash every module after.
#
# Usage:
#   ./scripts/generate_certs.sh --ip 192.168.1.100
#   ./scripts/generate_certs.sh --ip 192.168.1.100 --dns portunus.local
#   ./scripts/generate_certs.sh --sign-only --ip 10.0.0.57 --san DNS:localhost --san IP:127.0.0.1
#   ./scripts/generate_certs.sh --new-ca --ip 192.168.1.100
#
# Options:
#   --ip ADDR      server IPv4 address (SAN and CN); prompted for if omitted
#   --dns NAME     add a DNS name to the SAN
#   --san ENTRY    add a raw SAN entry such as IP:127.0.0.1 (repeatable)
#   --days N       server cert validity in days (default 825)
#
# Output (in <repo>/certs/, gitignored):
#   ca.key, server.key   private keys (KEEP SECRET; ca.key never leaves this machine)
#   ca.pem               CA certificate, also copied to access_module/certs/ca_cert.pem
#   server.pem           server certificate, for PORTUNUS_TLS_CERT_FILE
#
# Requires: openssl >= 1.1.1
# ──────────────────────────────────────────────────────────────────────────────
set -euo pipefail

# Keys must never exist with group/other permissions, not even briefly.
umask 077

CA_DAYS=3650          # CA valid for 10 years (long-lived, rotated manually)
SERVER_DAYS=825       # Server cert valid ~2.25 years (Apple max)
KEY_BITS=2048
SERVER_IP=""
EXTRA_SAN=()
MODE="auto"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
OUT_DIR="${PORTUNUS_CERTS_DIR:-$REPO_ROOT/certs}"
FW_CERT_DIR="${PORTUNUS_FW_CERT_DIR:-$REPO_ROOT/access_module/certs}"

usage() { sed -n '2,/^# Requires/p' "$0" | sed 's/^# \{0,1\}//'; }

need_arg() {
    if [[ $# -lt 2 || -z "$2" || "$2" == --* ]]; then
        echo "Error: $1 needs a value (try --help)" >&2
        exit 1
    fi
}

set_mode() {
    if [[ "$MODE" != "auto" && "$MODE" != "$1" ]]; then
        echo "Error: --sign-only and --new-ca are mutually exclusive" >&2
        exit 1
    fi
    MODE="$1"
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --ip)        need_arg "$@"; SERVER_IP="$2"; shift 2 ;;
        --dns)       need_arg "$@"; EXTRA_SAN+=("DNS:$2"); shift 2 ;;
        --san)       need_arg "$@"; EXTRA_SAN+=("$2"); shift 2 ;;
        --days)      need_arg "$@"; SERVER_DAYS="$2"; shift 2 ;;
        --sign-only) set_mode sign; shift ;;
        --new-ca)    set_mode rotate; shift ;;
        --help|-h)   usage; exit 0 ;;
        *)
            echo "Unknown option: $1 (try --help)" >&2
            exit 1
            ;;
    esac
done

if ! [[ "$SERVER_DAYS" =~ ^[1-9][0-9]*$ ]]; then
    echo "Error: --days must be a positive integer" >&2
    exit 1
fi

# ── Decide what to do about the CA ───────────────────────────────────────────
HAVE_CA=0
[[ -f "$OUT_DIR/ca.key" || -f "$OUT_DIR/ca.pem" ]] && HAVE_CA=1

case "$MODE" in
    auto)
        if [[ $HAVE_CA -eq 1 ]]; then
            cat >&2 <<EOF
Error: a CA already exists in $OUT_DIR.
Every flashed module pins it, so this script will not replace it implicitly.
  New server cert, same CA (new IP, renewal):  --sign-only
  Rotate the CA (reflash every module after):  --new-ca
EOF
            exit 1
        fi
        MODE=create
        ;;
    sign)
        if [[ ! -f "$OUT_DIR/ca.key" || ! -f "$OUT_DIR/ca.pem" ]]; then
            echo "Error: --sign-only needs $OUT_DIR/ca.key and ca.pem" >&2
            exit 1
        fi
        ;;
esac

# ── Prompt for IP if not provided ────────────────────────────────────────────
if [[ -z "$SERVER_IP" ]]; then
    DEFAULT_IP=""
    if command -v ip &>/dev/null; then
        DEFAULT_IP=$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src") print $(i+1)}' || true)
    fi
    if [[ ! -t 0 ]]; then
        echo "Error: --ip is required when not running interactively." >&2
        exit 1
    fi
    if [[ -n "$DEFAULT_IP" ]]; then
        read -rp "Server LAN IP address [$DEFAULT_IP]: " SERVER_IP
        SERVER_IP="${SERVER_IP:-$DEFAULT_IP}"
    else
        read -rp "Server LAN IP address: " SERVER_IP
    fi
fi

valid_ipv4() {
    local IFS=. octet
    local -a parts
    [[ "$1" =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}$ ]] || return 1
    read -ra parts <<<"$1"
    for octet in "${parts[@]}"; do
        (( 10#$octet <= 255 )) || return 1
    done
}

if ! valid_ipv4 "$SERVER_IP"; then
    echo "Error: '$SERVER_IP' is not a valid IPv4 address." >&2
    exit 1
fi

SAN="IP:${SERVER_IP}"
for entry in "${EXTRA_SAN[@]+"${EXTRA_SAN[@]}"}"; do
    SAN="${SAN},${entry}"
done

# ── Rotation: move the old material aside, never delete it ───────────────────
if [[ "$MODE" == rotate && $HAVE_CA -eq 1 ]]; then
    BACKUP="$OUT_DIR/backup-$(date +%Y%m%d-%H%M%S)"
    mkdir -p "$BACKUP"
    find "$OUT_DIR" -maxdepth 1 -type f -exec mv -t "$BACKUP" {} +
    echo "→ Moved the previous CA and certs to $BACKUP"
fi

mkdir -p "$OUT_DIR"

echo ""
echo "Portunus TLS certificates"
echo "  Mode:          $MODE"
echo "  SAN:           $SAN"
echo "  Cert validity: $SERVER_DAYS days"
echo "  Output:        $OUT_DIR/"
echo ""

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

cat > "$WORK/ext.cnf" <<EOF
[v3_server]
basicConstraints       = CA:FALSE
keyUsage               = digitalSignature, keyEncipherment
extendedKeyUsage       = serverAuth
subjectAltName         = ${SAN}
subjectKeyIdentifier   = hash
authorityKeyIdentifier = keyid,issuer
EOF

# ── Step 1: CA (create / rotate only) ────────────────────────────────────────
if [[ "$MODE" != sign ]]; then
    echo "→ Generating CA key and self-signed certificate (${CA_DAYS}d)..."
    openssl genrsa -out "$OUT_DIR/ca.key" "$KEY_BITS" 2>/dev/null
    openssl req -new -x509 \
        -key "$OUT_DIR/ca.key" \
        -out "$OUT_DIR/ca.pem" \
        -days "$CA_DAYS" \
        -subj "/C=US/O=Portunus/CN=Portunus CA" \
        -addext "basicConstraints=critical,CA:TRUE,pathlen:0" \
        -addext "keyUsage=critical,keyCertSign,cRLSign" \
        2>/dev/null
fi

# ── Step 2: server certificate, built in WORK and moved in only once valid ───
echo "→ Generating and signing server certificate (${SERVER_DAYS}d)..."
openssl genrsa -out "$WORK/server.key" "$KEY_BITS" 2>/dev/null
openssl req -new -key "$WORK/server.key" -out "$WORK/server.csr" \
    -subj "/C=US/O=Portunus/CN=${SERVER_IP}" 2>/dev/null
openssl x509 -req \
    -in "$WORK/server.csr" \
    -CA "$OUT_DIR/ca.pem" \
    -CAkey "$OUT_DIR/ca.key" \
    -CAserial "$WORK/ca.srl" -CAcreateserial \
    -out "$WORK/server.pem" \
    -days "$SERVER_DAYS" \
    -extfile "$WORK/ext.cnf" \
    -extensions v3_server \
    2>/dev/null

if ! openssl verify -CAfile "$OUT_DIR/ca.pem" -verify_ip "$SERVER_IP" "$WORK/server.pem" >/dev/null 2>&1; then
    echo "✗ Chain or IP verification failed; nothing in $OUT_DIR was changed." >&2
    exit 1
fi

if [[ "$MODE" == sign && -f "$OUT_DIR/server.pem" ]]; then
    STAMP=$(date +%Y%m%d-%H%M%S)
    mv "$OUT_DIR/server.pem" "$OUT_DIR/server.pem.$STAMP"
    [[ -f "$OUT_DIR/server.key" ]] && mv "$OUT_DIR/server.key" "$OUT_DIR/server.key.$STAMP"
    echo "→ Kept the previous server pair as server.{pem,key}.$STAMP"
fi
mv "$WORK/server.key" "$OUT_DIR/server.key"
mv "$WORK/server.pem" "$OUT_DIR/server.pem"
chmod 600 "$OUT_DIR/ca.key" "$OUT_DIR/server.key"
chmod 644 "$OUT_DIR/ca.pem" "$OUT_DIR/server.pem"

# ── Step 3: CA into the firmware tree (unchanged by --sign-only) ────────────
if [[ "$MODE" != sign ]]; then
    mkdir -p "$FW_CERT_DIR"
    cp "$OUT_DIR/ca.pem" "$FW_CERT_DIR/ca_cert.pem"
    chmod 644 "$FW_CERT_DIR/ca_cert.pem"
    echo "→ Copied CA cert to $FW_CERT_DIR/ca_cert.pem"
fi

echo ""
echo "Server certificate:"
openssl x509 -in "$OUT_DIR/server.pem" -noout -subject -enddate | sed 's/^/  /'
openssl x509 -in "$OUT_DIR/server.pem" -noout -ext subjectAltName 2>/dev/null | grep -v "Subject Alternative" | sed 's/^/  /'
echo "  ✓ validates against ca.pem for $SERVER_IP"
echo ""
echo "Next steps:"
echo "  - Install server.pem and server.key on the server as root:portunus 640"
echo "    (docs/setup_server.md, 'Running as a systemd service'), then restart it."
if [[ "$MODE" == sign ]]; then
    echo "  - The CA is unchanged: modules need no reflash."
else
    echo "  - Rebuild and flash firmware so it pins this CA:"
    echo "      task firmware:build:prod && task firmware:flash"
    if [[ "$MODE" == rotate ]]; then
        echo "  - CA ROTATED: every module still pins the old CA until reflashed."
    fi
fi
