#!/usr/bin/env bash
set -euo pipefail

usage() {
    echo "usage: $0 IMAGE_ARCHIVE_DIR" >&2
    exit 2
}

[[ $# -eq 1 ]] || usage
IMAGE_DIR="$1"
command -v trivy >/dev/null 2>&1 || {
    echo "trivy is required to verify image archives" >&2
    exit 1
}

expected_manifest=$'amd64\tlinux/amd64\tnovavei-linux-amd64.tar\tnovavei-candidate:amd64\n386\tlinux/386\tnovavei-linux-386.tar\tnovavei-candidate:386\narm64\tlinux/arm64\tnovavei-linux-arm64.tar\tnovavei-candidate:arm64\narm-v7\tlinux/arm/v7\tnovavei-linux-arm-v7.tar\tnovavei-candidate:arm-v7'
actual_manifest="$(cat "$IMAGE_DIR/IMAGES.tsv")"
[[ "$actual_manifest" == "$expected_manifest" ]] || {
    echo "unexpected image archive manifest" >&2
    diff -u <(printf '%s\n' "$expected_manifest") "$IMAGE_DIR/IMAGES.tsv" >&2 || true
    exit 1
}

revision="$(cat "$IMAGE_DIR/SOURCE_SHA")"
version="$(cat "$IMAGE_DIR/IMAGE_VERSION")"
[[ "$revision" =~ ^[0-9a-f]{40}$ ]] || {
    echo "invalid SOURCE_SHA" >&2
    exit 1
}
[[ -n "$version" ]] || {
    echo "IMAGE_VERSION is empty" >&2
    exit 1
}

(
    cd "$IMAGE_DIR"
    sha256sum -c ARCHIVES.sha256
)

: >"$IMAGE_DIR/IMAGE_IDS.tsv"
while IFS=$'\t' read -r slug platform archive local_ref; do
    archive_path="$IMAGE_DIR/$archive"
    [[ -s "$archive_path" ]] || {
        echo "missing image archive: $archive_path" >&2
        exit 1
    }

    trivy image \
        --input "$archive_path" \
        --scanners vuln \
        --pkg-types os,library \
        --severity HIGH,CRITICAL \
        --ignore-unfixed \
        --exit-code 1 \
        --no-progress \
        --format table
    trivy image \
        --input "$archive_path" \
        --scanners vuln \
        --format cyclonedx \
        --output "$IMAGE_DIR/sbom-${slug}.cdx.json" \
        --no-progress

    docker image rm "$local_ref" >/dev/null 2>&1 || true
    docker load --input "$archive_path" >/dev/null

    os="$(docker image inspect "$local_ref" --format '{{.Os}}')"
    arch="$(docker image inspect "$local_ref" --format '{{.Architecture}}')"
    variant="$(docker image inspect "$local_ref" --format '{{.Variant}}')"
    case "$platform" in
        linux/amd64) [[ "$os/$arch" == "linux/amd64" && -z "$variant" ]] ;;
        linux/386) [[ "$os/$arch" == "linux/386" && -z "$variant" ]] ;;
        linux/arm64) [[ "$os/$arch" == "linux/arm64" && -z "$variant" ]] ;;
        linux/arm/v7) [[ "$os/$arch/$variant" == "linux/arm/v7" ]] ;;
        *) echo "unsupported platform in manifest: $platform" >&2; exit 1 ;;
    esac

    [[ "$(docker image inspect "$local_ref" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}')" == "$revision" ]]
    [[ "$(docker image inspect "$local_ref" --format '{{index .Config.Labels "org.opencontainers.image.version"}}')" == "$version" ]]
    [[ "$(docker image inspect "$local_ref" --format '{{index .Config.Labels "org.opencontainers.image.source"}}')" == "https://github.com/kingsunb/NovaVei" ]]
    image_id="$(docker image inspect "$local_ref" --format '{{.Id}}')"
    printf '%s\t%s\t%s\t%s\t%s\n' "$slug" "$platform" "$archive" "$local_ref" "$image_id" >>"$IMAGE_DIR/IMAGE_IDS.tsv"

    scripts/smoke-test-image.sh "$local_ref" "novavei-${slug}-smoke" "$platform"
done <"$IMAGE_DIR/IMAGES.tsv"

(
    cd "$IMAGE_DIR"
    sha256sum \
        SOURCE_SHA IMAGE_VERSION IMAGES.tsv IMAGE_IDS.tsv ARCHIVES.sha256 \
        novavei-linux-amd64.tar novavei-linux-386.tar \
        novavei-linux-arm64.tar novavei-linux-arm-v7.tar \
        sbom-amd64.cdx.json sbom-386.cdx.json \
        sbom-arm64.cdx.json sbom-arm-v7.cdx.json \
        >PUBLISH.sha256
    sha256sum -c PUBLISH.sha256
)

echo "image archives scanned and smoke tested: ${IMAGE_DIR}"
