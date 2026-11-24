"""Pure Python policy tests; every lsblk/mount command is replaced by a fake."""
import json
import http.client
import os
from pathlib import Path
import tempfile
import threading
import unittest
from unittest.mock import patch

from service import Volumes, Rejected, Server, Handler


class VolumeTests(unittest.TestCase):
    def setUp(self):
        self.directory=tempfile.TemporaryDirectory(prefix='filehub-host-storage-')
        self.root=Path(self.directory.name)
        self.base=self.root/'mounts';self.base.mkdir()
        self.uuid='1234-ABCD';self.mounted=False;self.commands=[]
        self.nodes=[self.node()]
        self.volumes=Volumes(self.root/'volumes.json',self.base,os.getuid(),os.getgid(),run=self.fake_command)

    def tearDown(self):self.directory.cleanup()

    def node(self,**values):
        return dict(name='/dev/fake-test-device',type='part',fstype='ext4',uuid='1234-ABCD',
                    label='Synthetic data',size=12*2**30,mountpoints=[],ro=False,**values)

    def fake_command(self,args):
        self.commands.append(args)
        if args[0]=='lsblk':
            nodes=json.loads(json.dumps(self.nodes))
            if self.mounted:nodes[0]['mountpoints']=[str(self.base/self.uuid)]
            return json.dumps({'blockdevices':nodes}).encode()
        if args[0]=='mount':
            self.mounted=True
            return b''
        raise AssertionError('Unexpected external command')

    def attach_fake(self):
        # Test-only bypass of root-owned host directory checks. No mount syscall runs.
        with patch.object(self.volumes,'check_base'),patch.object(self.volumes,'prepare_target',side_effect=lambda p:p.mkdir(exist_ok=True)):
            return self.volumes.attach(self.uuid)

    def test_only_exact_enumerated_uuid_and_fixed_mount_options_are_accepted(self):
        result=self.attach_fake()
        self.assertTrue(result['mounted'])
        mounts=[c for c in self.commands if c[0]=='mount']
        self.assertEqual(mounts,[['mount','--types','ext4','--options','nodev,nosuid,noexec','--source','UUID='+self.uuid,'--target',str(self.base/self.uuid)]])
        self.assertTrue((self.base/self.uuid/'Filehub').is_dir())
        self.assertEqual(json.loads((self.root/'volumes.json').read_text()),[self.uuid])

    def test_system_disk_and_its_other_partitions_are_protected(self):
        self.nodes=[{'name':'/dev/fake-system','type':'disk','children':[dict(self.node(),mountpoints=['/']),dict(self.node(),uuid='OTHER-UUID')]}]
        self.assertTrue(all(not item['canMount'] for item in self.volumes.inventory()))
        with self.assertRaises(Rejected):self.attach_fake()
        self.assertFalse(any(c[0]=='mount' for c in self.commands))

    def test_existing_mount_other_than_owned_target_is_never_remounted(self):
        self.nodes[0]['mountpoints']=['/existing/owner/data']
        with self.assertRaises(Rejected):self.attach_fake()
        self.assertFalse(any(c[0]=='mount' for c in self.commands))

    def test_duplicate_uuids_fail_closed(self):
        self.nodes.append(dict(self.node(),name='/dev/another-fake'))
        self.assertTrue(all(not item['canMount'] for item in self.volumes.inventory()))
        with self.assertRaises(Rejected):self.attach_fake()

    def test_blank_encrypted_and_readonly_devices_are_not_formatted(self):
        for change in ({'uuid':None,'fstype':None},{'fstype':'crypto_LUKS'},{'ro':True},{'type':'lvm'}):
            with self.subTest(change=change):
                self.nodes=[dict(self.node(),**change)]
                with self.assertRaises(Rejected):self.attach_fake()
        self.assertFalse(any(c[0]!='lsblk' for c in self.commands))

    def test_request_cannot_inject_device_path_or_shell(self):
        for uuid in ('../../etc','/dev/sda','1234;touch /tmp/file','$(id)','unknown-uuid'):
            with self.subTest(uuid=uuid),self.assertRaises(Rejected):self.volumes.attach(uuid)
        self.assertFalse(any(c[0]=='mount' for c in self.commands))

    def test_existing_filehub_directory_and_contents_are_never_chowned(self):
        target=self.base/self.uuid;target.mkdir();(target/'Filehub').mkdir()
        original=target/'Filehub'/'keep.txt';original.write_text('original fixture')
        self.volumes.desired=[self.uuid];self.mounted=True
        with patch.object(self.volumes,'check_base'),patch('service.os.chown') as chown:
            self.volumes.attach(self.uuid)
            chown.assert_not_called()
        self.assertEqual(original.read_text(),'original fixture')

    def test_writable_or_nonempty_fallback_target_is_rejected(self):
        target=self.base/self.uuid;target.mkdir()
        with self.assertRaises(Rejected):self.volumes.prepare_target(target)
        (target/'preserve.txt').write_text('keep')
        with self.assertRaises(Rejected):self.volumes.prepare_target(target)
        self.assertEqual((target/'preserve.txt').read_text(),'keep')

    def test_disconnected_registered_volume_remains_registered(self):
        self.volumes.desired=[self.uuid];self.nodes=[]
        self.volumes.reconnect()
        self.assertEqual(self.volumes.desired,[self.uuid])
        self.assertFalse(any(c[0]=='mount' for c in self.commands))

    def test_loop_devices_need_an_explicit_uuid_allowlist(self):
        self.nodes[0]['type']='loop'
        self.assertFalse(self.volumes.inventory()[0]['canMount'])
        self.volumes.allow_loop=True
        self.assertFalse(self.volumes.inventory()[0]['canMount'])
        self.volumes.allowed={self.uuid}
        self.assertTrue(self.volumes.inventory()[0]['canMount'])
        self.volumes.allowed={'OTHER-UUID'}
        self.assertEqual(self.volumes.inventory(),[])

    def test_lxc_receives_guest_path_and_filesystem_identity(self):
        self.volumes.exposed_base=Path('/guest/filehub-disks')
        result=self.attach_fake()
        self.assertEqual(result['mount'],'/guest/filehub-disks/'+self.uuid)
        device=(self.base/self.uuid).stat().st_dev
        self.assertEqual(result['filesystemDevice'],str(os.major(device))+':'+str(os.minor(device)))

    def test_repeated_shared_mount_path_does_not_trigger_another_mount(self):
        target=self.base/self.uuid;target.mkdir()
        self.nodes[0]['mountpoints']=[str(target),str(target)]
        self.volumes.desired=[self.uuid]
        with patch.object(self.volumes,'check_base'):
            self.assertTrue(self.volumes.attach(self.uuid)['mounted'])
        self.assertFalse(any(command[0]=='mount' for command in self.commands))

    def test_foreign_mount_cannot_be_hidden_by_another_mount(self):
        target=self.base/self.uuid;target.mkdir()
        with patch('service.os.path.ismount',return_value=True):
            with self.assertRaises(Rejected):self.volumes.prepare_target(target)

    def test_app_writable_parent_is_not_allowed_as_mount_base(self):
        with self.assertRaises(Rejected):self.volumes.check_base()

    def test_socket_api_rejects_unknown_operations_and_extra_mount_parameters(self):
        import socket
        path=self.root/'control.sock';server=Server(str(path),Handler);server.volumes=self.volumes
        thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start()
        def post(route,data):
            connection=http.client.HTTPConnection('localhost');connection.sock=socket.socket(socket.AF_UNIX,socket.SOCK_STREAM);connection.sock.connect(str(path))
            connection.request('POST',route,json.dumps(data),{'Content-Type':'application/json'})
            response=connection.getresponse();response.read();status=response.status;connection.close();return status
        try:
            self.assertEqual(post('/v1/format',{'uuid':self.uuid}),404)
            self.assertEqual(post('/v1/attach',{'uuid':self.uuid,'options':'anything'}),400)
            self.assertEqual(post('/v1/attach',{'uuid':self.uuid,'target':'/etc'}),400)
            self.assertEqual(self.commands,[])
        finally:
            server.shutdown();server.server_close();thread.join()


if __name__=='__main__':unittest.main()
