# Security

FileHub is a self-hosted preview for a trusted home network or a private VPN.
Keep the FileHub port inside that network. For HTTPS deployments, configure TLS
at a reverse proxy you control.

## Reporting

Use **Security → Report a vulnerability** on GitHub for a private report. If the
form is unavailable, contact the repository owner through a private channel.
Include the affected version, installation type, access required and the
behavior you observed. Use sample files and test accounts. Keep credentials,
live databases and personal media out of the report.

## Boundaries

- Unrestricted profiles share configured storage sources. A restricted profile
  receives access only to its grants. A writable personal folder may be assigned
  within those sources. Web, Android and WebDAV enforce grants on the server.
- Uploaded originals must not execute with the application's browser privileges.
- Internal `.filehub-*` state is excluded from ordinary file APIs and archives.
- Initial account creation requires an operator-only setup code. CSRF alone is
  not proof of installation ownership. Existing accounts must not be reset by an update.
- The supported service definitions run FileHub unprivileged. Legacy root units
  require a separate migration. The optional root storage helper accepts
  bounded operations for existing eligible filesystems; it does not format disks.
- Passwords must only be sent to the server selected for that authentication
  attempt. Android updates must preserve the installed signing identity.
- The Android background photo queue requires HTTPS and checks the configured
  server's certificate. Manual file access can use HTTP in a trusted home network.
- Recovery and phone-delete decisions require checked bytes, not only a pathname
  or an upload-success response. Phone deletion verifies copies over HTTPS or a
  direct HTTP connection to a private IPv4 address on the same trusted home
  network. Home HTTP relies on the LAN being trusted. Preserve `(source, path)` identities.

## Releases

Source inspection does not establish release readiness. Native changes require
server tests, Android tests/lint, signed artifact checks and an isolated end-to-end
installation before a replacement package is promoted. Dependency vulnerability
checks and release verification are tracked separately from source review.
