# Android performance update — 1.40.3 (versionCode 53)

The Android-only update works with the existing Filehub 1.40.2 server. It does
not change server storage roots, photo scopes, upload receipts or originals.

- Saved accounts open their local UI before server authentication completes.
  Parallel 401 responses share a single login for the selected recipient.
- The existing SQLite photo cache migrates in place. Normalized columns avoid
  reparsing every JSON row. An unchanged server token needs no delta or catalog reload.
- Paging 3 loads 240 timeline items at a time, with placeholders, bounded retained
  pages (960 items), and a separate date index for distant date jumps. Day/month
  headers remain global. A photo viewer or explicit batch action can still read
  its full selected list on a worker thread; this is not constant memory for every action.
- Account-scoped ViewModels retain photo pages and bounded folder listings between
  tabs. Saveable state retains folder, query, grid and list positions.
- Local total/backup counters share one MediaStore scan, invalidated by changes,
  permissions, selected albums, and backup completion. They do not replace the
  receipt reconciliation and local hash checks required before deleting originals.
- Map filtering, GeoJSON construction, key indexing and selection sorting run on
  worker threads. Cluster leaves cross the MapLibre UI/JNI boundary in batches of
  240; the selection offers “Load more”. Map SDK lifecycle calls stay on the UI thread.

## Device measurements and app-specific Baseline Profiles

No phone is connected for this update. No startup milliseconds, FPS improvement,
physical-device acceptance or generated app-specific profile is claimed. AndroidX
library profiles and ProfileInstaller remain packaged in release APKs.

The optional `:benchmark` module contains shared navigation/scroll journeys,
`BaselineProfileGenerator` and `StartupBenchmark`. It is excluded unless
`-PfilehubBenchmarks=true` is supplied. Automatic profile generation is disabled;
normal release assembly does not require USB and never launches an emulator.

Use a dedicated test installation on an authorized physical Android 13+ device,
with synthetic account/media, saved login and app lock disabled. Do not run a
benchmark against the owner's normal phone installation: installing an instrumented
variant or resetting compilation may disturb its state. Keep all trace output
private; traces may contain screen and path data. Never put credentials in Gradle
arguments or the repository. Preserve the installed signing certificate when
preparing any update APK.

On the approved Windows build machine, inside the existing aggregate Job Object
(6 GiB RAM / 25% CPU, no parallel build jobs), run:

```powershell
# Compile test APKs only; no device operation:
gradle -PfilehubBenchmarks=true :benchmark:assembleBenchmarkRelease `
  :benchmark:assembleNonMinifiedRelease
# With a separately authorized, prepared test phone:
gradle -PfilehubBenchmarks=true :app:generateReleaseBaselineProfile
gradle -PfilehubBenchmarks=true :benchmark:connectedBenchmarkReleaseAndroidTest `
  -Pandroid.testInstrumentationRunnerArguments.class=com.filehub.benchmark.StartupBenchmark
```

Do not use the producer module’s aggregate `:benchmark:assemble`: with this
plugin it also schedules connected profile collection. The two explicit APK tasks
above compile without a device; verify their task graph using `--dry-run`.

Check the variant task names using `:benchmark:tasks --all` if the plugin changes.
Review and commit generated `app/src/release/generated/baselineProfiles` rules;
then rebuild and compare identical journeys on the same device before/after,
including cold startup, frame timing, cached/offline startup, tab return and a
large gallery. Profile generation and Macrobenchmark have not been run for this
release without a phone. The build verification report records actual compiled tasks.

Official references: [Paging](https://developer.android.com/topic/libraries/architecture/paging/v3-overview),
[profile generation](https://developer.android.com/topic/performance/baselineprofiles/create-baselineprofile),
[plugin configuration](https://developer.android.com/topic/performance/baselineprofiles/configure-baselineprofiles),
[Macrobenchmark](https://developer.android.com/topic/performance/benchmarking/macrobenchmark-overview).
