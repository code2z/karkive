# KArkive Stream runner for engine redis: redis-cli 7.4 (RDB over the replication protocol).
# Built by .github/workflows/runners.yaml only when runner/redis/ or runner/install-rclone.sh changes. The image tag is runner/redis/TAG: <client><version>-rclone<version>-r<revision>. Bump the revision for any other change.
ARG BASE=docker.io/library/redis:7.4.12-bookworm
FROM ${BASE}
ARG RCLONE_VERSION=1.76.0
ARG RCLONE_SHA256_AMD64=b00f00b3266c3764413afe599d3ec316f6f43b3e0ea8a0bc1d186d2133e46df7
ARG RCLONE_SHA256_ARM64=ee169016675e23a69d77240565133ff7dbc9b33d16fa8f7053d4af39f1eaeb78
ARG TARGETARCH
COPY install-rclone.sh /usr/local/sbin/install-rclone.sh
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates curl unzip gnupg age zstd; \
    /usr/local/sbin/install-rclone.sh; \
    apt-get purge -y --auto-remove curl unzip; \
    rm -rf /var/lib/apt/lists/* /usr/local/sbin/install-rclone.sh; \
    redis-cli --version; zstd --version; gpg --version | head -n 1; age --version; rclone version | head -n 1
USER 26:26
ENTRYPOINT []
CMD ["/bin/bash"]
