#!/usr/bin/env bash
# deploy_remote.sh: the server-side half of task deploy:server / rollback.
#
# scripts/deploy_server.sh streams this file over SSH and runs it as root
# (`sudo bash -s -- ACTION key=value...`). It is not meant to be run by hand.
#
# Actions:
#   install    verify the staged binary's SHA-256, stop the service, snapshot
#              the database, install the binary, start, health check; on a
#              failed health check put the previous binary back.
#   rollback   previous binary back (and the pre-deploy DB snapshot only with
#              restore_db=1), start, health check.
#
# Keys: dir= service= staged= sha256= version= restore_db= health_seconds= db=
set -euo pipefail

ACTION="${1:?action required}"
shift
DIR=/opt/portunus SERVICE=portunus-server STAGED="" SHA="" VERSION=unknown
RESTORE_DB=0 HEALTH_SECONDS=10 DB=""
for kv in "$@"; do
    case "$kv" in
        dir=*)            DIR="${kv#*=}" ;;
        service=*)        SERVICE="${kv#*=}" ;;
        staged=*)         STAGED="${kv#*=}" ;;
        sha256=*)         SHA="${kv#*=}" ;;
        version=*)        VERSION="${kv#*=}" ;;
        restore_db=*)     RESTORE_DB="${kv#*=}" ;;
        health_seconds=*) HEALTH_SECONDS="${kv#*=}" ;;
        db=*)             DB="${kv#*=}" ;;
        *) echo "deploy_remote: unknown argument $kv" >&2; exit 2 ;;
    esac
done

BIN="$DIR/bin/portunus-server"
STATE="$DIR/deploy"           # version files and the last-backup pointer
mkdir -p "$STATE"
OWNER=()
[[ "$(id -u)" == 0 ]] && OWNER=(-o root -g root)

log() { echo "[remote] $*"; }

# The database path comes from the unit's EnvironmentFile (PORTUNUS_DB_PATH),
# resolved against its WorkingDirectory when relative. Only that one key is
# read; the file also holds secrets and is never printed.
resolve_db() {
    [[ -n "$DB" ]] && return
    local envfile wd
    envfile=$(systemctl show -p EnvironmentFiles --value "$SERVICE" | awk '{print $1}')
    if [[ -n "$envfile" && -r "$envfile" ]]; then
        DB=$(sed -n 's/^[[:space:]]*PORTUNUS_DB_PATH=//p' "$envfile" | tail -1 | tr -d "\"'")
    fi
    DB="${DB:-./data/portunus.db}"
    if [[ "$DB" != /* ]]; then
        wd=$(systemctl show -p WorkingDirectory --value "$SERVICE")
        DB="${wd:-/}/${DB#./}"
    fi
}

# Healthy = active for the whole window with the same main PID, i.e. it did
# not crash and get restarted by Restart=on-failure. "is-active" right after
# a restart is not enough: a simple service is active the instant it forks.
health_check() {
    local pid now i
    sleep 1
    pid=$(systemctl show -p MainPID --value "$SERVICE")
    for ((i = 0; i < HEALTH_SECONDS; i++)); do
        if ! systemctl is-active --quiet "$SERVICE"; then
            log "health: $SERVICE is not active"
            return 1
        fi
        now=$(systemctl show -p MainPID --value "$SERVICE")
        if [[ "$pid" == 0 || "$now" != "$pid" ]]; then
            log "health: main PID changed ($pid -> $now): it restarted"
            return 1
        fi
        sleep 1
    done
    log "health: active and stable for ${HEALTH_SECONDS}s (pid $pid)"
}

snapshot_db() {
    local stamp dest f
    resolve_db
    if [[ ! -f "$DB" ]]; then
        log "no database at $DB yet; nothing to snapshot"
        return
    fi
    stamp=$(date +%Y%m%d-%H%M%S)
    dest="$(dirname "$DB")/backups/pre-deploy-$stamp-${VERSION//[^A-Za-z0-9._-]/_}"
    mkdir -p "$dest"
    # The service is stopped, so the main file plus any -wal/-shm is a
    # consistent copy without needing sqlite3 on the server.
    for f in "$DB" "$DB-wal" "$DB-shm"; do
        [[ -f "$f" ]] && cp -a "$f" "$dest/"
    done
    echo "$dest" > "$STATE/last-db-backup"
    log "database snapshot: $dest"
}

restore_db() {
    local src stamp f
    src=$(cat "$STATE/last-db-backup" 2>/dev/null || true)
    if [[ -z "$src" || ! -d "$src" ]]; then
        log "no pre-deploy snapshot recorded; database left as is"
        return 1
    fi
    resolve_db
    stamp=$(date +%Y%m%d-%H%M%S)
    # Move the current files aside instead of deleting them.
    for f in "$DB" "$DB-wal" "$DB-shm"; do
        [[ -f "$f" ]] && mv "$f" "$f.rolled-back-$stamp"
    done
    for f in "$src"/*; do
        cp -a "$f" "$(dirname "$DB")/"
    done
    log "database restored from $src (previous files kept as *.rolled-back-$stamp)"
}

put_back_previous() {
    if [[ ! -f "$BIN.prev" ]]; then
        log "no previous binary ($BIN.prev) to put back"
        return 1
    fi
    install -m 755 "${OWNER[@]}" "$BIN.prev" "$BIN"
    [[ -f "$STATE/version.prev" ]] && cp "$STATE/version.prev" "$STATE/version"
    log "previous binary reinstalled ($(cat "$STATE/version" 2>/dev/null || echo unknown version))"
}

case "$ACTION" in
    install)
        [[ -f "$STAGED" ]] || { log "staged binary $STAGED missing"; exit 1; }
        got=$(sha256sum "$STAGED" | awk '{print $1}')
        if [[ "$got" != "$SHA" ]]; then
            log "SHA-256 mismatch for staged binary (got $got); aborting, nothing changed"
            exit 1
        fi

        # Only a binary that is running healthily becomes the rollback target:
        # deploying twice in a row with a bad build must not lose the good one.
        if [[ -f "$BIN" ]] && systemctl is-active --quiet "$SERVICE"; then
            install -m 755 "${OWNER[@]}" "$BIN" "$BIN.prev"
            [[ -f "$STATE/version" ]] && cp "$STATE/version" "$STATE/version.prev"
            log "kept running binary as rollback target"
        else
            log "service not active: keeping the existing rollback target"
        fi

        systemctl stop "$SERVICE"
        snapshot_db
        install -m 755 "${OWNER[@]}" "$STAGED" "$BIN"
        echo "$VERSION" > "$STATE/version"
        systemctl start "$SERVICE"
        log "installed $VERSION, checking health"

        if health_check; then
            log "deploy OK: $VERSION"
            exit 0
        fi
        log "deploy FAILED health check; rolling back the binary"
        journalctl -u "$SERVICE" -n 20 --no-pager 2>/dev/null | sed 's/^/[journal] /' || true
        systemctl stop "$SERVICE" || true
        if put_back_previous; then
            systemctl start "$SERVICE"
            health_check || log "previous binary is not healthy either: investigate now"
        fi
        log "database NOT restored automatically (snapshot: $(cat "$STATE/last-db-backup" 2>/dev/null || echo none))."
        log "If the failed version migrated the schema: task deploy:server:rollback RESTORE_DB=1"
        exit 1
        ;;

    rollback)
        # Check before stopping, so a refused rollback leaves the service up.
        [[ -f "$BIN.prev" ]] || { log "no previous binary ($BIN.prev); nothing to roll back to"; exit 1; }
        if [[ "$RESTORE_DB" == 1 && ! -d "$(cat "$STATE/last-db-backup" 2>/dev/null || true)" ]]; then
            log "RESTORE_DB=1 but no pre-deploy snapshot is recorded; aborting, nothing changed"
            exit 1
        fi
        systemctl stop "$SERVICE"
        put_back_previous
        if [[ "$RESTORE_DB" == 1 ]]; then
            restore_db
        fi
        systemctl start "$SERVICE"
        health_check
        log "rollback OK"
        ;;

    *)
        echo "deploy_remote: unknown action $ACTION" >&2
        exit 2
        ;;
esac
