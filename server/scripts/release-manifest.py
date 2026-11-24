#!/usr/bin/env python3
"""Reproducible source/artifact identity, with no private configuration."""
from pathlib import Path
import hashlib
import json
import subprocess
import sys

root = Path(__file__).resolve().parents[1]
out = Path(sys.argv[1]).resolve()
from source_identity import collect_sources
sha = lambda p: hashlib.sha256(p.read_bytes()).hexdigest()
sources = collect_sources(root)
source_hash = hashlib.sha256(json.dumps(sources, sort_keys=True, separators=(',', ':')).encode()).hexdigest()
web = {p.relative_to(root / 'web').as_posix(): sha(p) for p in sorted((root / 'web').rglob('*')) if p.is_file()}
version = json.loads((root / 'release.json').read_text())['version']
result = dict(sourceCommit=subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip(), version=version, buildId=version + '-' + source_hash[:12], sourceSha256=source_hash,
              sourceFiles=sources, binarySha256=sha(out / 'filehub'), web=web,
              goVersion=subprocess.check_output(['go', 'version'], text=True).strip(),
              nodeVersion=subprocess.check_output(['node', '--version'], text=True).strip())
(out / 'filehub-build.json').write_text(json.dumps(result, indent=2) + '\n')
print(json.dumps({key: result[key] for key in ('version', 'buildId', 'binarySha256')}))
