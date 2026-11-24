package com.filehub.backup

import android.app.Application
import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Context
import android.os.Build
import coil.ImageLoader
import coil.ImageLoaderFactory
import coil.disk.DiskCache
import coil.memory.MemoryCache

const val BACKUP_CHANNEL = "backup"

/** App-wide shared Coil ImageLoader (uses the auth'd OkHttp client + real disk
 *  cache so thumbnails are fast and survive restarts) + the backup notif channel. */
class App : Application(), ImageLoaderFactory {
    override fun onCreate() {
        super.onCreate()
        Net.onSessionChanged = { coil.Coil.reset() }
        if (Build.VERSION.SDK_INT >= 26) {
            val ch = NotificationChannel(
                BACKUP_CHANNEL, appResources().getString(R.string.transfer_ui_backup_channel),
                NotificationManager.IMPORTANCE_LOW,
            )
            val manager = getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
            manager.createNotificationChannel(ch)
            manager.createNotificationChannel(NotificationChannel(OfflineDownload.Channel,
                appResources().getString(R.string.downloads_title), NotificationManager.IMPORTANCE_LOW))
        }
        // Photos now live under one folder (HDD2/Photos); move the backup target inside
        // it so new phone photos land in the scoped timeline. One-time, old default only.
        Prefs(this).let { if (it.folder == "PhoneBackup") it.folder = "Photos/PhoneBackup" }
        if (Prefs(this).auto) Backup.setAuto(this, true) // re-arm instant backup after process/boot start
    }

    override fun newImageLoader(): ImageLoader =
        ImageLoader.Builder(this)
            .okHttpClient(Net.client)
            // Grid thumbs are opaque JPEG → RGB_565 halves each bitmap's heap + GC cost
            // (the concrete "downloading = lag" mechanism). No-op for images with alpha.
            .allowRgb565(true)
            .memoryCache { MemoryCache.Builder(this@App).maxSizePercent(0.30).build() }
            .diskCache { DiskCache.Builder().directory(cacheDir.resolve("image_cache")).maxSizeBytes(256L * 1024 * 1024).build() }
            // OFF globally — correct for a grid surface (no fade storm as cells fill).
            // The full-screen viewer opts back in per-request. Do NOT move the memory-cache
            // lookup off-main (interceptorDispatcher) — Coil checks it synchronously so
            // scroll-back is instant; moving it reintroduces a flash.
            .crossfade(false)
            .respectCacheHeaders(false) // thumb cache key already encodes mtime+size+width → no re-download
            .build()
}
