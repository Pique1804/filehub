# Android 1.40.3 verification

Prepared 2026-11-16. Android versionCode **53**; compatible with the existing
Filehub **1.40.2** server. This is an Android update in the combined repository;
no server executable or database migration is required.

## Verified artifacts

- Compiled application source: `ba275f1075c821deaff0d6fbc4ef66b1c6b51218`.
- Signed release APK: **53,387,937 bytes**.
- SHA256: `e4b51f793b1b136db65b82b28d9a6674733284de56758e3a33d7008416db6246`.
- Existing signing certificate retained:
  `15fc6661c4196a4732d7a055140b9d01b697c8fc15ef169617070ba953dff5b2`.
- Package `com.filehub.backup`, minSdk26, release is not debuggable.
- APK contains compiled AndroidX library profiles (`baseline.prof`, `baseline.profm`).
  No app-specific profile was generated without a connected test device.

Native compilation ran only on the approved Windows machine, inside a Windows
Job Object limiting all descendants together to **6 GiB RAM / 25% CPU**, with
one Gradle worker.

## Checks

- **26 Android JVM tests passed**, zero failures/errors/skips. These include
  account-recipient/logout guards, concurrent 401 reauthentication, backup receipt
  verification, persistent cache migration on simulated Android9, atomic delta
  failure handling, account/favorite isolation, global date headers, page bounds,
  distant jumps and prepend boundaries.
- Release lint: **0 errors, 29 warnings**; no lint baseline or suppression added.
- R8 minified release APK assembled, zip-aligned, signed and independently verified.
- Both benchmark test APK variants compiled with
  `:benchmark:assembleBenchmarkRelease :benchmark:assembleNonMinifiedRelease`.
  The final task graph was checked with `--dry-run` and contains no connected-device
  tasks. The producer's aggregate `:benchmark:assemble` must not be used for this:
  an initial attempt scheduled connected profile collection and stopped with
  “No connected devices”; no device test ran.
- Publication rehearsal used the exact old and new APKs. Apply, rollback, rejection
  of insufficient disk space, and rollback after an injected manifest-swap failure
  passed. The server and database sentinels stayed unchanged. This verifies the
  distribution swap, not an installed Android downgrade.

## Pending on a physical phone

Installation, app startup/frame timings, tab/date navigation, partial photo
permissions, backup behavior and native map interaction still need device
acceptance. No measured speedup is claimed. Dedicated Baseline Profile generation
and Macrobenchmark remain prepared, unexecuted device work; see
[the performance workflow](android-performance.md).

The photo cache migrates additively to version2; backup receipts and original media
are untouched. Android normally rejects lowering versionCode. Returning the old
APK on the server only changes the offered download; it does not downgrade a phone
already on code53. Use a forward fix for any device-specific regression unless a
separate device recovery has been agreed.

During the deployment check, the APK and version manifest
were published on the existing Filehub1.40.2 server. Its PID and executable stayed
unchanged; no restart was required. The old APK/manifest were retained and the
live new checksums passed. One explicitly approved, verified temporary rehearsal
DB duplicate was removed to free space; the working database and upgrade backups
were preserved. Physical-phone installation remains unverified.

No repository merge, public release or paid Actions run was performed.
