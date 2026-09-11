#!/bin/sh
# Dockerfile 已固定 USER 10001:10001。入口只收紧新建文件权限并校验唯一
# 可写目录, 不修改镜像 rootfs, 不递归 chown 数据。
set -eu

umask 0077

if [ ! -d /app/data ] || [ ! -w /app/data ]; then
    echo "error: /app/data must be writable by UID:GID 10001:10001" >&2
    echo "prepare a host directory with mode 0700 and owner 10001:10001, or use a fresh named volume" >&2
    exit 1
fi

cd /app
exec "/app/${APP_NAME:-novaveil}" "$@"
