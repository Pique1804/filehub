#!/usr/bin/env python3
"""Package a built release with input hashes and its preserved signing identity."""
from pathlib import Path
import datetime
import hashlib
import json
import re
import shutil
import subprocess
import sys

project=Path(__file__).resolve().parents[1]
destination=Path(sys.argv[1]).resolve()
apk=project / "app/build/outputs/apk/release/app-release.apk"
expected="15fc6661c4196a4732d7a055140b9d01b697c8fc15ef169617070ba953dff5b2"
certificate=subprocess.check_output([sys.argv[2],"verify","--print-certs",str(apk)],text=True)
signer=re.search(r"Signer #1 certificate SHA-256 digest: ([a-f0-9]+)",certificate).group(1)
if signer != expected:
    raise SystemExit("Signing identity differs from installed Filehub: release rejected")
metadata=json.loads((apk.parent / "output-metadata.json").read_text())["elements"][0]
name=f"filehub-{metadata['versionName']}.apk"
source_files=[]
for path in sorted(project.rglob("*")):
    if not path.is_file() or any(part in {"build",".gradle",".git","__pycache__"} for part in path.relative_to(project).parts):
        continue
    relative=path.relative_to(project).as_posix()
    allowed_roots={"README.md","build.gradle.kts","settings.gradle.kts","gradle.properties",
                   "gradlew","gradlew.bat","app/build.gradle.kts","app/proguard-rules.pro"}
    if relative not in allowed_roots and not relative.startswith(("app/src/","tests/","scripts/","gradle/")):
        continue
    if path.suffix in {".jks",".keystore"}:
        continue
    source_files.append({"path":relative,"sha256":hashlib.sha256(path.read_bytes()).hexdigest()})
source_hash=hashlib.sha256(json.dumps(source_files,separators=(",",":"),ensure_ascii=False).encode()).hexdigest()
digest=hashlib.sha256(apk.read_bytes()).hexdigest()
destination.mkdir(parents=True,exist_ok=True)
shutil.copy2(apk,destination/name)
(destination/(name+".sha256")).write_text(f"{digest}  {name}\n")
manifest={"applicationId":"com.filehub.backup","versionName":metadata["versionName"],
    "versionCode":metadata["versionCode"],"apk":name,"size":apk.stat().st_size,"sha256":digest,
    "signerSha256":signer,"sourceSha256":source_hash,"sourceFiles":source_files,
    "createdAt":datetime.datetime.now(datetime.timezone.utc).isoformat(),
    "toolchain":{"java":17,"gradle":"8.7","agp":"8.5.2","kotlin":"1.9.24","compileSdk":34}}
root_gradle=(project/"build.gradle.kts").read_text()
app_gradle=(project/"app/build.gradle.kts").read_text()
manifest["sourceCommit"]=subprocess.check_output(["git","rev-parse","HEAD"],cwd=project,text=True).strip()
manifest["toolchain"].update(
    agp=re.search(r'id\("com.android.application"\) version "([^"]+)"',root_gradle).group(1),
    kotlin=re.search(r'id\("org.jetbrains.kotlin.android"\) version "([^"]+)"',root_gradle).group(1),
    compileSdk=int(re.search(r"compileSdk\s*=\s*(\d+)",app_gradle).group(1)),
)
(destination/f"filehub-{metadata['versionName']}-manifest.json").write_text(json.dumps(manifest,indent=2,ensure_ascii=False)+"\n")
print(f"{name}: {digest}; preserved signing certificate: {signer}")
