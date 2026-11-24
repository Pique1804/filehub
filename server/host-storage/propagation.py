"""Prepare only Filehub's dedicated mount subtree for container propagation."""
import os
from pathlib import Path
import subprocess
import sys

base=Path(sys.argv[1])
if os.geteuid()!=0 or not base.is_absolute() or base==Path('/') or base.resolve()!=base:
    raise SystemExit('Invalid Filehub mount root')
base.mkdir(parents=True,mode=0o755,exist_ok=True)
for parent in (base,*base.parents):
    info=parent.stat()
    if info.st_uid!=0 or info.st_mode&0o022:raise SystemExit('Mount root must be controlled by root')
if subprocess.run(['mountpoint','--quiet',str(base)]).returncode:
    subprocess.run(['mount','--bind',str(base),str(base)],check=True)
subprocess.run(['mount','--make-rshared',str(base)],check=True)
