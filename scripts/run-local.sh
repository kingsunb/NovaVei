#!/bin/bash
# 用最新本地构建产物启动 NovaVei，并做一次 HTTP 冒烟。
# 保留 data/（配置、SQLite、管理员密码），只替换正在跑的进程。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"

PORT="${NOVAVEI_SERVER_PORT:-8080}"
HOST="${NOVAVEI_SERVER_HOST:-0.0.0.0}"
BIN=""
for candidate in \
    "${ROOT}/build/bin/novavei-linux-amd64" \
    "${ROOT}/build/bin/novavei-linux-arm64" \
    "${ROOT}/novavei"; do
    if [ -x "${candidate}" ]; then
        BIN="${candidate}"
        break
    fi
done
if [ -z "${BIN}" ]; then
    echo "no binary found. run: bash scripts/build.sh --local" >&2
    exit 1
fi

LOG="${ROOT}/logs/novavei.log"
mkdir -p "${ROOT}/data" "${ROOT}/logs"

stop_old() {
    local pids
    pids="$(pgrep -f "${ROOT}/build/bin/novavei-" 2>/dev/null || true)"
    if [ -z "${pids}" ]; then
        pids="$(pgrep -f "${ROOT}/novavei start" 2>/dev/null || true)"
    fi
    if [ -n "${pids}" ]; then
        echo "stopping previous instance: ${pids}"
        # shellcheck disable=SC2086
        kill ${pids} 2>/dev/null || true
        sleep 1
        # shellcheck disable=SC2086
        kill -9 ${pids} 2>/dev/null || true
    fi
}

wait_listen() {
    local i
    for i in $(seq 1 20); do
        if ss -ltn 2>/dev/null | grep -q ":${PORT} "; then
            return 0
        fi
        sleep 0.3
    done
    echo "service did not listen on ${PORT} within timeout" >&2
    tail -n 40 "${LOG}" >&2 || true
    return 1
}

stop_old
echo "starting ${BIN} on ${HOST}:${PORT}"
NOVAVEI_SERVER_HOST="${HOST}" NOVAVEI_SERVER_PORT="${PORT}" \
    nohup "${BIN}" start >>"${LOG}" 2>&1 &
echo $! >"${ROOT}/logs/novavei.pid"
wait_listen

code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "http://127.0.0.1:${PORT}/" || true)"
if [ "${code}" != "200" ]; then
    echo "smoke failed: GET / returned HTTP ${code:-none}" >&2
    tail -n 40 "${LOG}" >&2 || true
    exit 1
fi

echo "ok  http://127.0.0.1:${PORT}/  (HTTP ${code})"
echo "log ${LOG}"
if [ -f "${ROOT}/data/initial-admin-password" ]; then
    echo "bootstrap password: ${ROOT}/data/initial-admin-password"
fi
if [ -x "${BIN}" ]; then
    "${BIN}" version || true
fi
