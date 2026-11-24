#!/usr/bin/env bash
set -euo pipefail
project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
artifact_dir="${1:?Usage: scripts/release.sh /absolute/artifact/directory}"
gradle_bin="${GRADLE_BIN:-gradle}"
sdk_dir="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-$HOME/android-sdk}}"
signing_file="${FILEHUB_SIGNING_KEYSTORE:?Set the existing FileHub publisher keystore path}"
: "${FILEHUB_SIGNING_STORE_PASSWORD:?Set the publisher keystore password}"
: "${FILEHUB_SIGNING_KEY_ALIAS:?Set the publisher key alias}"
: "${FILEHUB_SIGNING_KEY_PASSWORD:?Set the publisher key password}"
test -f "$signing_file" || { echo 'Existing signing key is required; do not generate a replacement.' >&2; exit 1; }
cd "$project_dir"
"$gradle_bin" --no-daemon --max-workers=1 :app:testDebugUnitTest :app:lintRelease :app:assembleRelease
python3 scripts/release_manifest.py "$artifact_dir" "$sdk_dir/build-tools/34.0.0/apksigner"
