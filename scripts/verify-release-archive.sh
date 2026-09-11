#!/bin/sh
# 非 Docker 更新辅助: 在解压/替换现有二进制之前, 强制按 Release 同目录的
# SHA256SUMS 校验归档。SHA256 可检测下载损坏/篡改, 但不是发布者签名;
# 签名验证未在本脚本内实现, 残余风险见 docs/SECURE_DEPLOYMENT.md。
set -eu

usage() {
    echo "usage: $0 ARCHIVE SHA256SUMS" >&2
    exit 2
}

[ "$#" -eq 2 ] || usage
ARCHIVE="$1"
CHECKSUMS="$2"

[ -f "${ARCHIVE}" ] || {
    echo "error: archive not found: ${ARCHIVE}" >&2
    exit 1
}
[ -f "${CHECKSUMS}" ] || {
    echo "error: checksum file not found: ${CHECKSUMS}" >&2
    exit 1
}

archive_name="$(basename "${ARCHIVE}")"
archive_dir="$(CDPATH= cd -- "$(dirname -- "${ARCHIVE}")" && pwd)"
checksum_abs="$(CDPATH= cd -- "$(dirname -- "${CHECKSUMS}")" && pwd)/$(basename "${CHECKSUMS}")"

# 只保留精确匹配本归档 basename 的一行, 防止恶意文件名触发路径穿越。
expected_line="$(awk -v file="${archive_name}" '{ name=$2; sub(/^\*/, "", name); sub(/^\.\//, "", name); if (name == file) { print; found=1 } } END { if (!found) exit 1 }' "${checksum_abs}")" || {
    echo "error: ${archive_name} is not listed in ${CHECKSUMS}" >&2
    exit 1
}

printf '%s\n' "${expected_line}" | (cd "${archive_dir}" && sha256sum -c -)
echo "verified: ${archive_name}"
