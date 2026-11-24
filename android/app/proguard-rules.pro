# R8/minify keep rules for the release build.

# WorkManager instantiates Worker/CoroutineWorker subclasses by class name (reflection).
-keep class * extends androidx.work.ListenableWorker { public <init>(...); }


# media3 / ExoPlayer + OkHttp datasource ship their own consumer rules; silence stray warnings.
-dontwarn okhttp3.**
-dontwarn okio.**
-dontwarn org.conscrypt.**

# Keep the app's Application + JobService/Activity (also referenced from the manifest).
-keep class com.filehub.backup.App { *; }
-keep class com.filehub.backup.MainActivity { *; }
-keep class com.filehub.backup.MediaWatchJob { *; }
