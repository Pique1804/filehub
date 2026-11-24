#!/usr/bin/env bash
set -euo pipefail
# The server and APK are published artifacts. This command never builds sources.
cd "$(dirname -- "${BASH_SOURCE[0]}")/.."
if [[ ${1-} == --install-dependencies ]]; then
  shift
  [[ $EUID -eq 0 ]] || { echo 'Запустите установку через sudo.' >&2; exit 1; }
  filehub_missing=0
  for filehub_dependency in python3 nginx nft lsblk mount findmnt ffmpeg tesseract vipsthumbnail rsync apksigner aapt; do
    command -v "$filehub_dependency" >/dev/null || filehub_missing=1
  done
  python3 -c 'import ctypes.util; assert ctypes.util.find_library("crypt")' >/dev/null 2>&1 || filehub_missing=1
  if [[ $filehub_missing -eq 1 ]]; then
    filehub_had_nginx=0
    command -v nginx >/dev/null && filehub_had_nginx=1
    command -v apt-get >/dev/null || { echo 'Автоустановка пакетов поддерживает Debian/Ubuntu; установите зависимости вручную.' >&2; exit 1; }
    systemd-run --wait --pipe --collect --property=MemoryMax=384M --property=MemorySwapMax=0 --property=CPUQuota=50% \
      /usr/bin/apt-get update
    systemd-run --wait --pipe --collect --property=MemoryMax=384M --property=MemorySwapMax=0 --property=CPUQuota=50% --setenv=DEBIAN_FRONTEND=noninteractive \
      /usr/bin/apt-get install -y --no-install-recommends -o Dpkg::Options::=--force-confold python3 libcrypt1 nginx nftables util-linux ffmpeg tesseract-ocr tesseract-ocr-eng tesseract-ocr-rus libvips-tools rsync apksigner aapt
    if [[ $filehub_had_nginx -eq 0 ]]; then systemctl disable --now nginx.service; fi
  fi
fi
command -v nginx >/dev/null || { echo 'Требуются готовые пакеты python3 и nginx.' >&2; exit 1; }
exec python3 distribution/prebuilt/configure.py "$@"
