#!/usr/bin/env bash
# server_smoke.sh: boot the server in the ci profile and check that HTTPS and
# gRPC TLS come up (task ci:server:smoke).
#
# A real bash script, not inline Taskfile shell: Task's interpreter sets $! to
# a goroutine label ("g1"), not a PID, so `kill $!` there never stopped the
# server and every local run leaked one onto ports 8443/50051.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../server"

bash ../scripts/stop_local_servers.sh
# Anything still listening is not this checkout's build: it would answer the
# checks below in place of the server under test, so refuse to start.
for port in 8443 50051; do
    if (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null; then
        echo "smoke FAILED: port $port is already in use by another process"
        exit 1
    fi
done
go build -o bin/portunus-server ./cmd/portunus-server
set -a; . ./.env.ci; set +a

./bin/portunus-server &
SRV=$!
cleanup() {
    kill "$SRV" 2>/dev/null || true
    wait "$SRV" 2>/dev/null || true
}
trap cleanup EXIT
trap 'exit 130' INT TERM HUP

code=""
for _ in $(seq 1 30); do
    code=$(curl -ksS -o /dev/null -w '%{http_code}' https://127.0.0.1:8443/admin/ui/login || true)
    [[ "$code" == "200" ]] && break
    sleep 0.2
done
[[ "$code" == "200" ]] || { echo "HTTPS smoke FAILED (last code: ${code:-none})"; exit 1; }
# The server under test must still be running (not exited on a listen error).
kill -0 "$SRV" 2>/dev/null || {
    echo "HTTPS smoke FAILED: the server under test exited"
    exit 1
}
echo "HTTPS OK: /admin/ui/login -> 200 over TLS"
if echo | openssl s_client -connect 127.0.0.1:50051 -alpn h2 2>/dev/null | grep -q "BEGIN CERTIFICATE"; then
    echo "gRPC TLS OK: certificate presented on :50051"
else
    echo "gRPC TLS smoke FAILED"
    exit 1
fi
