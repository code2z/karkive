#!/bin/sh
# Installs a pinned, checksum-verified rclone into /usr/local/bin. Shared by every runner image.
set -eu
: "${RCLONE_VERSION:?}" "${RCLONE_SHA256_AMD64:?}" "${RCLONE_SHA256_ARM64:?}"
case "${TARGETARCH:-amd64}" in
  amd64) sum="${RCLONE_SHA256_AMD64}" ;;
  arm64) sum="${RCLONE_SHA256_ARM64}" ;;
  *) echo "unsupported arch ${TARGETARCH}" >&2; exit 1 ;;
esac
zip="rclone-v${RCLONE_VERSION}-linux-${TARGETARCH:-amd64}.zip"
curl -fsSL -o "/tmp/${zip}" "https://github.com/rclone/rclone/releases/download/v${RCLONE_VERSION}/${zip}"
echo "${sum}  /tmp/${zip}" | sha256sum -c -
unzip -q -j "/tmp/${zip}" "*/rclone" -d /usr/local/bin
chmod 0755 /usr/local/bin/rclone
rm -f "/tmp/${zip}"
