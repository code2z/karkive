# KArkive Stream runner for engine pvc: GNU tar for PVC file archives.
# Built by .github/workflows/runners.yaml only when runner/pvc/ or runner/install-rclone.sh changes. The image tag is runner/pvc/TAG: <client><version>-rclone<version>-r<revision>. Bump the revision for any other change.
ARG BASE=docker.io/library/debian:trixie-20260918-slim
FROM ${BASE}
ARG RCLONE_VERSION=1.76.0
ARG RCLONE_SHA256_AMD64=b00f00b3266c3764413afe599d3ec316f6f43b3e0ea8a0bc1d186d2133e46df7
ARG RCLONE_SHA256_ARM64=ee169016675e23a69d77240565133ff7dbc9b33d16fa8f7053d4af39f1eaeb78
ARG TARGETARCH
COPY install-rclone.sh /usr/local/sbin/install-rclone.sh
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates curl unzip gnupg age zstd tar bash; \
    /usr/local/sbin/install-rclone.sh; \
    apt-get purge -y --auto-remove curl unzip; \
    rm -rf /var/lib/apt/lists/* /usr/local/sbin/install-rclone.sh; \
    tar --version | head -n 1; zstd --version; gpg --version | head -n 1; age --version; rclone version | head -n 1
USER 26:26
ENTRYPOINT []
CMD ["/bin/bash"]
