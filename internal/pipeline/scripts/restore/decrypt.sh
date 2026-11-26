set -eu
umask 0002
STAGE=decrypt
# Shared PVC is reused across Jobs; scope scratch to this pod
# so peers never see stale .step-* markers from a prior run.
WORKDIR_ROOT="${WORKDIR}"
WORKDIR="${WORKDIR_ROOT}/${HOSTNAME}"
pipeline_init
wait_for "${WORKDIR}/.step-fetch-done" "fetch"
already_done_hold "${WORKDIR}/.step-decrypt-done" "decrypt"
log "stage start: decrypt method=${ENCRYPTION_METHOD}"
IN="${WORKDIR}/dump.gz.${ENC_EXT}"
test -s "${IN}"
log "${ENCRYPTION_METHOD} decrypt $(basename "${IN}") ($(wc -c < "${IN}") bytes)"
if [ "${ENCRYPTION_METHOD}" = age ]; then
  AGE_IDENTITY_FILE="${AGE_IDENTITY_FILE:-/run/secrets/age/age_identity}"
  test -s "${AGE_IDENTITY_FILE}"
  age --decrypt --identity "${AGE_IDENTITY_FILE}" --output "${WORKDIR}/dump.gz" "${IN}"
else
  export GNUPGHOME="/tmp/gpg-home"
  mkdir -p "$GNUPGHOME" && chmod 700 "$GNUPGHOME"
  gpg --batch --yes --decrypt \
    --passphrase-file /run/secrets/gpg/gpg_passphrase \
    --output "${WORKDIR}/dump.gz" \
    "${IN}"
fi
rm -f "${IN}"
log "decrypt done size=$(wc -c < "${WORKDIR}/dump.gz") bytes"
touch "${WORKDIR}/.step-decrypt-done"
log "wrote marker .step-decrypt-done; stage work done"
hold_until_job_done
