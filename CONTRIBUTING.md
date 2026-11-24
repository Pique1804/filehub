# Contributing

Filehub is one product in one repository:

- `server/`: Go API, Svelte web client, Linux installer and optional storage helper.
- `android/`: Kotlin/Compose Android client and upload/backup logic.
- `docs/`: installation, storage, resources and development notes.

Read [development notes](docs/development.md) before changing the version or
packaging a release. Keep fixes focused and include a regression test when a
security or data-integrity boundary changes. Use synthetic accounts and temporary
files. Never use household media or production credentials in tests.

Preserve source/path identities, original files, existing accounts and the Android
signing identity. Do not commit generated APKs/binaries, database snapshots,
private keys, session state or signing keystores. Report security issues privately
as described in [SECURITY.md](SECURITY.md).

Use the [issue tracker](https://github.com/Pique1804/filehub/issues) for product
bugs and feature requests. For a bug, include your server and client versions,
how you installed Filehub and steps to reproduce it with sample data.

Run the checks described in the development notes in your own build environment.
GitHub's verification workflow runs on manual dispatch.
