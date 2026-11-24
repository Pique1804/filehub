"""Verify the downloadable host installer never includes installation state."""
import hashlib
import io
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest
from mounting import build_bundle, sign_host_tools


class BundleTests(unittest.TestCase):
    def test_bundle_is_repeatable_source_only_and_checksums_every_file(self):
        root=Path(__file__).resolve().parents[3]
        bundle=build_bundle(root)
        self.assertEqual(bundle,build_bundle(root))
        with tarfile.open(fileobj=io.BytesIO(bundle),mode='r:gz') as archive:
            files={item.name.removeprefix('filehub-host-tools/'):archive.extractfile(item).read() for item in archive}
        self.assertIn('server/host-storage/proxmox.py',files)
        self.assertIn('server/distribution/prebuilt/web/golos.woff2',files)
        self.assertIn('server/host-storage/connect-linux.py',files)
        self.assertFalse(any(name.endswith(('.apk','.db','.json','.tar.gz')) for name in files))
        recorded=files.pop('SHA256SUMS').decode().splitlines()
        self.assertEqual(len(recorded),len(files))
        for line in recorded:
            digest,name=line.split('  ',1)
            self.assertEqual(hashlib.sha256(files[name]).hexdigest(),digest)
        with tempfile.NamedTemporaryFile(suffix='.sh') as shell:
            shell.write(files['connect.sh']);shell.flush()
            subprocess.run(['bash','-n',shell.name],check=True)

    def test_host_tools_require_the_trusted_signature_before_unpacking(self):
        root = Path(__file__).resolve().parents[3]
        wrapper = root / 'server/scripts/connect-host.sh'
        bundle = build_bundle(root)
        with tempfile.TemporaryDirectory() as work:
            work = Path(work)
            key, pub = work / 'signing.pem', work / 'trusted.pem'
            archive, signature = work / 'kit.tar.gz', work / 'kit.sig'
            subprocess.run(['openssl', 'ecparam', '-genkey', '-name', 'prime256v1', '-out', str(key)], check=True)
            subprocess.run(['openssl', 'pkey', '-in', str(key), '-pubout', '-out', str(pub)], check=True)
            signature.write_bytes(sign_host_tools(bundle, key))
            archive.write_bytes(bundle)
            command = ['bash', str(wrapper), str(pub), str(archive), str(signature), '--help']
            result = subprocess.run(command, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn('--ctid', result.stdout)
            archive.write_bytes(bundle + b'\n')
            result = subprocess.run(command, capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertNotIn('--ctid', result.stdout)
