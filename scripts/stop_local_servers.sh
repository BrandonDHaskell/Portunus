#!/usr/bin/env bash
# stop_local_servers.sh: stop Portunus servers left running from this
# checkout before a task starts a new one.
#
# A server started by ci:server:smoke, run:server:*, or the hardware tier
# outlives its task if that task is killed (a timeout, a closed terminal,
# SIGKILL). The next run then finds the ports taken, or worse, its health
# check is answered by the stale build. Only processes owned by this user
# whose executable is one of this checkout's server builds, or a `go run`
# build of portunus-server, are touched.
#
# Usage: scripts/stop_local_servers.sh   (from anywhere in the checkout)
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

ours() {
    local exe=${1% (deleted)}
    case "$exe" in
        "$ROOT"/server/bin/portunus-server*) return 0 ;;
        "$ROOT"/access_module/test/hardware/build/hw-server) return 0 ;;
        */go-build*/exe/portunus-server) return 0 ;;
    esac
    return 1
}

pids=()
for d in /proc/[0-9]*; do
    [[ -O "$d" ]] || continue
    exe=$(readlink "$d/exe" 2>/dev/null) || continue
    if ours "$exe"; then
        pids+=("${d#/proc/}")
        echo "stopping stale server: pid ${d#/proc/} ($exe)"
    fi
done
[[ ${#pids[@]} -eq 0 ]] && exit 0

kill -TERM "${pids[@]}" 2>/dev/null || true
for _ in $(seq 1 50); do
    alive=()
    for p in "${pids[@]}"; do kill -0 "$p" 2>/dev/null && alive+=("$p"); done
    [[ ${#alive[@]} -eq 0 ]] && exit 0
    sleep 0.1
done
echo "still running after 5 s, sending SIGKILL: ${alive[*]}"
kill -KILL "${alive[@]}" 2>/dev/null || true
