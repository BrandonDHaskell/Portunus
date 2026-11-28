#!/usr/bin/env bash
# deploy_server.sh: push the arm64 server binary to the Pi, or roll it back.
#
# Usage (normally via task deploy:server / deploy:server:rollback):
#   DEPLOY_HOST=user@host scripts/deploy_server.sh deploy
#   DEPLOY_HOST=user@host [RESTORE_DB=1] scripts/deploy_server.sh rollback
#
# Environment:
#   DEPLOY_HOST      required, ssh target (key auth, passwordless sudo there)
#   DEPLOY_DIR       default /opt/portunus
#   DEPLOY_SERVICE   default portunus-server
#   DEPLOY_DB        database path override; default: PORTUNUS_DB_PATH from
#                    the unit's EnvironmentFile
#   HEALTH_SECONDS   how long the service must stay up, default 10
#   RESTORE_DB       rollback only: 1 also restores the pre-deploy DB snapshot
#                    (discards every write made since that deploy)
#
# The server side is scripts/deploy_remote.sh, streamed over SSH and run as
# root, so the remote steps are versioned with the code and testable locally.
set -euo pipefail

ACTION="${1:-}"
: "${DEPLOY_HOST:?DEPLOY_HOST is required (user@host); set it in .env or pass it to task}"
DEPLOY_DIR="${DEPLOY_DIR:-/opt/portunus}"
DEPLOY_SERVICE="${DEPLOY_SERVICE:-portunus-server}"
HEALTH_SECONDS="${HEALTH_SECONDS:-10}"

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REMOTE_SCRIPT="$ROOT/scripts/deploy_remote.sh"
BINARY="$ROOT/server/bin/portunus-server-linux-arm64"

COMMON=("dir=$DEPLOY_DIR" "service=$DEPLOY_SERVICE" "health_seconds=$HEALTH_SECONDS")
[[ -n "${DEPLOY_DB:-}" ]] && COMMON+=("db=$DEPLOY_DB")

run_remote() {
    # printf %q quotes each argument for the remote shell.
    ssh "$DEPLOY_HOST" "sudo bash -s -- $(printf '%q ' "$@")" < "$REMOTE_SCRIPT"
}

case "$ACTION" in
    deploy)
        [[ -f "$BINARY" ]] || { echo "missing $BINARY (task build:server:arm64)" >&2; exit 1; }
        VERSION=$(git -C "$ROOT" describe --always --dirty --tags 2>/dev/null || echo unknown)
        SHA=$(sha256sum "$BINARY" | awk '{print $1}')
        echo "Deploying $VERSION (sha256 ${SHA:0:16}...) to $DEPLOY_HOST:$DEPLOY_DIR"

        # A private staging dir owned by the deploy user, not a fixed /tmp path
        # another account on the host could pre-create or swap.
        STAGE=$(ssh "$DEPLOY_HOST" 'mktemp -d')
        trap 'ssh "$DEPLOY_HOST" "rm -rf $(printf %q "$STAGE")" || true' EXIT
        scp -q "$BINARY" "$DEPLOY_HOST:$STAGE/portunus-server"

        run_remote install "${COMMON[@]}" "staged=$STAGE/portunus-server" \
            "sha256=$SHA" "version=$VERSION"
        ;;
    rollback)
        run_remote rollback "${COMMON[@]}" "restore_db=${RESTORE_DB:-0}"
        ;;
    *)
        echo "usage: $0 deploy|rollback" >&2
        exit 2
        ;;
esac
