# Filehub build and recovery

The Go service, Svelte frontend and Android app share this repository. Android
sources are at `../android`; its signing identity must remain unchanged.

Run server commands from the `server/` directory.

Run `bash scripts/build-release.sh /absolute/output/directory` with Go, Node/npm,
Python 3 and rsync available. The script installs the locked frontend dependencies,
builds assets, runs the Go and backup-script checks, and produces a static binary
and `filehub-build.json`. It does not deploy, rewrite systemd units or bootstrap
credentials. Real EXIF/OCR and Android uploader integration tests require their
explicit synthetic fixtures and runtime tools.

The manifest records hashes of allowed source files, generated web assets and the
binary. Private environment files, signing keys, live SQLite databases and media
are excluded from Git and the distributable source manifest. Build with the
reported Go/Node versions to compare artifacts. Generated `web/` is regenerated
from `frontend/`; it is required before a direct `go build` due to embedding.

Deploy only after a private native SQLite backup and binary/source/configuration
backup. Preserve `/etc/filehub.env` and the existing systemd service. Stop the
service only for the checked binary swap, start it, then verify HTTP/authentication,
database invariants and the running executable hash. Database migrations in this
release only add alias-history storage. Roll back the executable and source if
needed; do not restore an older database over newer user activity.

### Standalone service account

The standalone unit under `distribution/filehub.service` runs as `filehub`
with private state in `/var/lib/filehub`. It can write to the two default data
mounts only where the operating-system permissions also grant access. Add any
custom writable mount to a systemd drop-in and grant the account the required
permissions on that mount. The build helpers preserve existing units and refuse
to restart a legacy service configured to run as root.

For a legacy installation, identify the live database, session secret, caches,
upload staging, backup destinations and SMB group permissions first. Keep the
configured path identities, back up the database and unit, and move or grant
access to the service state explicitly. Test real file and backup access with
the new account before putting the unit into service. Keep original file
ownership and scope any permission changes to the required directories.

`original-backup.py` preserves deleted/replaced originals in its destination. It
checks source mount availability, repairs checksum differences even when size and
mtime match, verifies edit versions, and creates a consistent checked SQLite
snapshot. It keeps the previous successful status if a later run fails. It never
prunes historical backups. Source and destination must be on different mounted
disks. Run its unit tests on temporary fixtures, never on household media.
