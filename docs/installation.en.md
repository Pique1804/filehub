# Install FileHub

Debian 12 x86-64 with systemd is the tested Linux installation path. The ready
archive includes the web server, setup wizard and signed Android APK. Android 8
or later can install the app. See [resource requirements](resources.md) before
setting up a large library.

## Install on Linux

Install the `curl` package if it is missing, then run the public installer.

```sh
curl -fsSL https://github.com/Pique1804/filehub/releases/latest/download/install.sh | bash
```

For a private release, sign in with `gh auth login` using an account with
repository access, then run:

```sh
gh release download -R Pique1804/filehub -p install.sh -O - | bash
```

The script verifies the archive's SHA-256 before starting the installer. To
install manually, download the Linux archive and its matching checksum from
[GitHub Releases](https://github.com/Pique1804/filehub/releases/latest).
In a folder containing just that archive and its checksum, run:

```sh
sha256sum -c filehub-*-linux-amd64*.tar.gz.sha256 &&
tar -xzf filehub-*-linux-amd64*.tar.gz &&
cd filehub &&
sudo bash ./install.sh --folders-only
```

Run `bash ./install.sh --folders-only` if you are already root. Use the
[Compose guide](compose.md) if you already run Docker and prefer bind-mounted
folders and separate state storage.

## Open the setup wizard

At the end, the terminal prints a private setup link, for example
`http://filehub.example.test:8080/setup#code=...`. Open the link shown on your
server from a browser that can reach it. Keep the actual setup code private.

1. Create your administrator account.
2. Select a server folder or check the attached disks you want to use. Expand a
   disk to select a folder.
3. Choose an existing or empty photo folder, or skip photos and configure them later.
4. Start FileHub. Upload a test file and download it back to verify access.
5. Use **Phone and storage → Storage** to add disks or photo folders after setup.

## Connect a physical disk in Proxmox LXC

To use a directory that Proxmox already mounted on the host, expose it to the
container as a mount point before installing FileHub. Then choose the directory
in the wizard. For the bundled disk manager, download and verify the same
archive on the Proxmox node. Open the node's **Shell**, unpack it there, and
run the following command with your container ID:

```sh
bash ./install.sh --proxmox CTID --restart
```

FileHub uses existing filesystems on eligible disks. Check the storage
selection before starting. [Storage guide](storage.md)

## Android and HTTPS

Choose **Download app** in the web sidebar and open the APK on the phone.
Enter the FileHub address and use your FileHub account. Use a trusted home
network or private VPN for access. For the background photo backup queue,
connect the app over HTTPS with a certificate the phone trusts. Manual
browsing and uploads also work over trusted home HTTP.

For a new native install from 1.40.22, prepare a TLS reverse proxy on the
same host, then run the installer with your own HTTPS origin:

```sh
sudo bash ./install.sh --folders-only --listen-address 127.0.0.1 \
  --url https://filehub.example.test
```

Configure the proxy to forward to `http://127.0.0.1:8080` and preserve the
original Host. The example name must be replaced with the name covered by
your certificate. Use the instructions in the linked release for earlier packages.

## Upgrade and recovery

Update an existing server using the instructions for the installed version.
Use the plan command to check space and keep a usable copy of the database,
originals and configuration. **Settings → App → Check for updates** on Android
checks the APK served by your FileHub. It becomes available after the server
administrator installs the matching package or publishes the APK there.
[Upgrade and recovery instructions](upgrading.md) · [FAQ](faq.md)
