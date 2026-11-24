#!/usr/bin/env bash
set -euo pipefail

repository=Pique1804/filehub
if [[ $(uname -s) != Linux || $(uname -m) != x86_64 ]]; then
    echo 'This FileHub installer requires Linux x86-64.' >&2
    exit 1
fi

for tool in python3 tar sha256sum; do
    if ! command -v "$tool" >/dev/null; then
        printf 'Install %s before running the FileHub installer.\n' "$tool" >&2
        exit 1
    fi
done

work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT

if command -v gh >/dev/null && gh api "repos/$repository/releases/latest" > "$work/release.json" 2>/dev/null; then
    use_github_cli=true
else
    use_github_cli=false
    if ! command -v curl >/dev/null ||
        ! curl -fsSL --proto '=https' "https://api.github.com/repos/$repository/releases/latest" -o "$work/release.json"; then
        echo 'Could not read the FileHub release. For a private repository, sign in with gh auth login.' >&2
        exit 1
    fi
fi

selection=$(python3 - "$work/release.json" <<'PY'
import json
import re
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    release = json.load(stream)
tag = release.get("tag_name", "")
if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9.-]*", tag):
    sys.exit("Could not identify the FileHub release tag.")

assets = {asset["name"] for asset in release["assets"]}
installers = [
    name for name in assets
    if re.fullmatch(r"filehub-[0-9][A-Za-z0-9.-]*-linux-amd64(?:-installer)?\.tar\.gz", name)
    and name + ".sha256" in assets
]
if len(installers) != 1:
    sys.exit("The release must contain one Linux x86-64 installer and its SHA-256 file.")
print(tag, installers[0])
PY
)
read -r tag archive <<< "$selection"

if "$use_github_cli"; then
    gh release download "$tag" -R "$repository" -D "$work" -p "$archive" -p "$archive.sha256"
else
    base="https://github.com/$repository/releases/download/$tag"
    curl -fsSL --proto '=https' --retry 2 "$base/$archive" -o "$work/$archive"
    curl -fsSL --proto '=https' --retry 2 "$base/$archive.sha256" -o "$work/$archive.sha256"
fi

cd "$work"
read -r checksum filename extra < "$archive.sha256"
if [[ ! $checksum =~ ^[[:xdigit:]]{64}$ || ${filename#\*} != "$archive" || -n $extra ]]; then
    echo 'The release checksum does not describe the downloaded installer.' >&2
    exit 1
fi
printf '%s  %s\n' "$checksum" "$archive" | sha256sum --check --strict -
tar -xzf "$archive"
cd filehub

if [[ $# -eq 0 ]]; then
    set -- --folders-only
fi
if [[ $EUID -eq 0 || ${1-} == --help ]]; then
    bash ./install.sh "$@"
else
    sudo bash ./install.sh "$@"
fi
