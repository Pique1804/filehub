"""Public build inputs only. Shared by manifest generation and packaging."""
import hashlib
from pathlib import Path


def collect_sources(root):
    root = Path(root)
    paths = list(root.glob('*.go')) + list(root.glob('*.py'))
    paths += [root/name for name in ('go.mod','go.sum','release.json','frontend/index.html','frontend/package.json','frontend/package-lock.json')]
    paths += [p for folder in ('scripts','frontend/src','frontend/public','clip-sidecar')
              for p in (root/folder).rglob('*') if p.is_file()]
    paths += list((root/'frontend').glob('*config.*'))
    return {p.relative_to(root).as_posix():hashlib.sha256(p.read_bytes()).hexdigest()
            for p in sorted(set(paths)) if p.is_file() and '__pycache__' not in p.parts}
