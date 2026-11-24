#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 4 ]]; then
  echo 'Usage: bash connect-host.sh TRUSTED-PUBLIC-KEY.pem KIT.tar.gz KIT.tar.gz.sig [host options]' >&2
  exit 2
fi

work_dir=$(mktemp -d)
trap 'rm -rf -- "$work_dir"' EXIT
cp -- "$1" "$work_dir/public.pem"
cp -- "$2" "$work_dir/kit.tar.gz"
cp -- "$3" "$work_dir/kit.sig"
shift 3

if ! openssl dgst -sha256 -verify "$work_dir/public.pem" -signature "$work_dir/kit.sig" "$work_dir/kit.tar.gz"; then
  echo 'The FileHub host tools signature is invalid.' >&2
  exit 1
fi

tar -xzf "$work_dir/kit.tar.gz" -C "$work_dir"
bash "$work_dir/filehub-host-tools/connect.sh" "$@"
