#!/usr/bin/env bash
set -euo pipefail

# Usage:
#   sudo bash scripts/setup_alarm_buffer_tmpfs.sh [MOUNT_POINT] [SIZE] [MODE] [PERSIST]
#
# Example:
#   sudo bash scripts/setup_alarm_buffer_tmpfs.sh /data/maas-recordings-buffer 1024m 0775 1
#
# Arguments:
#   MOUNT_POINT: tmpfs mount point (default: /data/maas-recordings-buffer)
#   SIZE:        tmpfs size (default: 1024m)
#   MODE:        mount mode (default: 0775)
#   PERSIST:     1=append /etc/fstab, 0=mount only (default: 1)

MOUNT_POINT="${1:-/data/maas-recordings-buffer}"
SIZE="${2:-1024m}"
MODE="${3:-0775}"
PERSIST="${4:-1}"

if [[ "${EUID}" -ne 0 ]]; then
  echo "ERROR: Please run as root (sudo)." >&2
  exit 1
fi

if [[ -z "${MOUNT_POINT}" ]]; then
  echo "ERROR: mount point is empty." >&2
  exit 1
fi

mkdir -p "${MOUNT_POINT}"

if mountpoint -q "${MOUNT_POINT}"; then
  CURRENT_FS="$(findmnt -n -o FSTYPE --target "${MOUNT_POINT}")"
  if [[ "${CURRENT_FS}" != "tmpfs" ]]; then
    echo "ERROR: ${MOUNT_POINT} is already mounted as ${CURRENT_FS}, not tmpfs." >&2
    exit 1
  fi
  echo "INFO: ${MOUNT_POINT} is already mounted as tmpfs."
else
  mount -t tmpfs -o "rw,nosuid,nodev,size=${SIZE},mode=${MODE}" tmpfs "${MOUNT_POINT}"
  echo "INFO: Mounted tmpfs at ${MOUNT_POINT} (size=${SIZE}, mode=${MODE})."
fi

if [[ "${PERSIST}" == "1" ]]; then
  FSTAB_LINE="tmpfs ${MOUNT_POINT} tmpfs rw,nosuid,nodev,size=${SIZE},mode=${MODE} 0 0"
  if grep -E "^[^#].*[[:space:]]${MOUNT_POINT}[[:space:]]+tmpfs[[:space:]].*$" /etc/fstab >/dev/null 2>&1; then
    echo "INFO: /etc/fstab already has an entry for ${MOUNT_POINT}, skipped."
  else
    cp /etc/fstab "/etc/fstab.bak.$(date +%Y%m%d%H%M%S)"
    printf "\n%s\n" "${FSTAB_LINE}" >> /etc/fstab
    echo "INFO: Added tmpfs entry to /etc/fstab."
  fi
fi

echo "INFO: Verification:"
findmnt --target "${MOUNT_POINT}" || true
df -h "${MOUNT_POINT}" || true

cat <<EOF

Next steps for maas-box:
1. Set BufferDir in configs/local/config.toml (dev) or configs/prod/config.toml (prod):
   BufferDir = "${MOUNT_POINT}"
2. Map the same host path to ZLM container path /recordings-buffer in compose:
   ${MOUNT_POINT}:/recordings-buffer
3. Restart Go and ZLM services.

EOF
