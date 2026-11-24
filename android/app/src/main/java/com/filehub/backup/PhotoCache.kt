package com.filehub.backup

import android.content.Context
import android.content.ContentValues
import android.database.Cursor
import android.database.sqlite.SQLiteDatabase
import android.database.sqlite.SQLiteOpenHelper
import okhttp3.Request
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody
import okhttp3.RequestBody.Companion.toRequestBody
import okio.Buffer
import org.json.JSONObject

/** Account-isolated cache. A partial refresh never replaces the visible snapshot. */
class PhotoCache(ctx: Context): SQLiteOpenHelper(ctx,"photo_cache.db",null,3), java.io.Closeable {
    private companion object {
        val refreshLock = Any()
        val jsonMediaType = "application/json".toMediaType()
        const val manifestBytes = 8L * 1024 * 1024
    }

    override fun onCreate(db: SQLiteDatabase) {
        db.execSQL("CREATE TABLE photos(account TEXT,pid TEXT,json TEXT,PRIMARY KEY(account,pid))")
        db.execSQL("CREATE TABLE snapshots(account TEXT PRIMARY KEY,token TEXT)")
        upgradeColumns(db)
        createStaging(db)
    }
    override fun onUpgrade(db: SQLiteDatabase,oldVersion: Int,newVersion: Int) {
        if(oldVersion<2) {
            upgradeColumns(db)
            db.rawQuery("SELECT account,pid,json FROM photos",null).use { c ->
                while(c.moveToNext()) db.update("photos",values(Api.parsePhoto(JSONObject(c.getString(2)))),"account=? AND pid=?",arrayOf(c.getString(0),c.getString(1)))
            }
        }
        if (oldVersion < 3) createStaging(db)
    }
    private fun createStaging(db: SQLiteDatabase) {
        db.execSQL("CREATE TABLE photo_staging AS SELECT * FROM photos WHERE 0")
        db.execSQL("CREATE UNIQUE INDEX photo_staging_identity ON photo_staging(account,pid)")
    }
    private fun upgradeColumns(db:SQLiteDatabase) {
        for(column in listOf("name TEXT","source TEXT","path TEXT","mod_time INTEGER","video INTEGER","revision TEXT","size INTEGER")) db.execSQL("ALTER TABLE photos ADD COLUMN $column")
        db.execSQL("CREATE INDEX photo_timeline ON photos(account,mod_time DESC,source,path)")
        db.execSQL("CREATE TABLE favorites(account TEXT,pid TEXT,PRIMARY KEY(account,pid))")
    }
    private fun values(p:Photo)=ContentValues().apply {
        put("name",p.name);put("source",p.source);put("path",p.path);put("mod_time",p.modTime)
        put("video",if(p.isVideo)1 else 0);put("revision",p.revision);put("size",p.size)
    }
    private fun filter(favorites:Boolean)=if(favorites) " AND pid IN (SELECT pid FROM favorites WHERE account=photos.account)" else ""
    private val order=" ORDER BY mod_time DESC,source,path"
    private fun Cursor.photo()=Photo(getString(0),getString(1),getString(2),getLong(3),getInt(4)!=0,getString(5),getLong(6))
    fun page(account:String,offset:Int,count:Int,favorites:Boolean=false):List<Photo> = readableDatabase.rawQuery("SELECT name,path,source,mod_time,video,revision,size FROM photos WHERE account=?"+filter(favorites)+order+" LIMIT ? OFFSET ?",arrayOf(account,count.toString(),offset.toString())).use { c -> buildList {while(c.moveToNext())add(c.photo())} }
    fun load(account:String,favorites:Boolean=false)=page(account,0,Int.MAX_VALUE,favorites)
    fun ids(account:String,favorites:Boolean=false):List<String> = readableDatabase.rawQuery("SELECT pid FROM photos WHERE account=?"+filter(favorites)+order,arrayOf(account)).use { c ->buildList {while(c.moveToNext())add(c.getString(0))} }
    fun selected(account:String,ids:Set<String>):List<Photo> = ids.toList().chunked(400).flatMap { batch ->
        readableDatabase.rawQuery("SELECT name,path,source,mod_time,video,revision,size FROM photos WHERE account=? AND pid IN (${batch.joinToString(","){"?"}})"+order,(listOf(account)+batch).toTypedArray()).use { c ->buildList {while(c.moveToNext())add(c.photo())} }
    }
    fun timeline(account:String,months:Boolean,favorites:Boolean):PhotoTimeline = readableDatabase.rawQuery("SELECT mod_time FROM photos WHERE account=?"+filter(favorites)+order,arrayOf(account)).use { c -> PhotoTimeline.build(sequence {while(c.moveToNext())yield(c.getLong(0))},months) }
    fun counts(account:String):Pair<Int,Int> = readableDatabase.rawQuery("SELECT COUNT(*),COALESCE(SUM(video),0) FROM photos WHERE account=?",arrayOf(account)).use {it.moveToFirst();it.getInt(0) to it.getInt(1)}
    fun favorites(account:String):Set<String> = readableDatabase.rawQuery("SELECT pid FROM favorites WHERE account=?",arrayOf(account)).use { c -> buildSet {while(c.moveToNext())add(c.getString(0))} }
    fun saveFavorites(account:String,ids:Set<String>) {
        val db=writableDatabase;db.beginTransaction()
        try {
            db.delete("favorites","account=?",arrayOf(account))
            for(id in ids)check(db.insert("favorites",null,ContentValues().apply {put("account",account);put("pid",id)})!=-1L)
            db.setTransactionSuccessful()
        }finally{db.endTransaction()}
    }
    /** Capture the recipient once: an account switch cannot redirect a refresh. */
    fun refresh(account: String, stillCurrent: () -> Boolean = { true }): Boolean = synchronized(refreshLock) {
        val (base, client) = Net.activeEndpoint()
        fun current() = client === Net.client && stillCurrent()
        fun json(path: String, body: RequestBody? = null): JSONObject {
            check(current())
            val request = Request.Builder().url(base + path).apply { if (body != null) post(body) }.build()
            return client.newCall(request).execute().use {
                check(it.isSuccessful) { "Photo refresh failed (${it.code})" }
                JSONObject(checkNotNull(it.body).string())
            }
        }
        val token = readableDatabase.rawQuery("SELECT token FROM snapshots WHERE account=?", arrayOf(account)).use {
            if (it.moveToFirst()) it.getString(0) else ""
        }
        val remote = json("/api/photos/token")
        val snapshot = remote.getString("token")
        if (token == snapshot) return@synchronized false
        val paged = remote.optBoolean("paged")
        val manifest = knownManifest(account)
        val delta = manifest?.let { json(if (paged) "/api/photos/delta?paged=1" else "/api/photos/delta", it) }
        if (delta == null || (paged && delta.optBoolean("snapshotRequired"))) {
            check(paged) { "Server does not support paged photo refresh" }
            refreshPages(account, snapshot, ::current) { cursor ->
                val page = (base + "/api/photos").toHttpUrl().newBuilder()
                    .addQueryParameter("snapshot", snapshot)
                    .addQueryParameter("after", cursor)
                    .build()
                json("/api/photos?" + page.encodedQuery)
            }
        } else {
            applyDelta(account, delta, ::current)
        }
        true
    }

    private fun knownManifest(account: String): RequestBody? {
        val body = Buffer().writeUtf8("{\"known\":{")
        readableDatabase.rawQuery("SELECT pid,revision,mod_time FROM photos WHERE account=?", arrayOf(account)).use { c ->
            while (c.moveToNext()) {
                if (c.position > 0) body.writeByte(','.code)
                body.writeUtf8(JSONObject.quote(c.getString(0))).writeByte(':'.code)
                    .writeUtf8(JSONObject.quote(c.getString(1) + ":" + c.getLong(2)))
                if (body.size + 2 > manifestBytes) return null
            }
        }
        return body.writeUtf8("}}").readByteString().toRequestBody(jsonMediaType)
    }

    private fun photoRow(account: String, json: JSONObject): ContentValues {
        val photo = Api.parsePhoto(json)
        return values(photo).apply {
            put("account", account)
            put("pid", photo.source + "|" + photo.path)
            put("json", json.toString())
        }
    }

    private fun saveToken(db: SQLiteDatabase, account: String, token: String) {
        val value = ContentValues().apply { put("account", account); put("token", token) }
        check(db.insertWithOnConflict("snapshots", null, value, SQLiteDatabase.CONFLICT_REPLACE) != -1L)
    }

    private fun applyDelta(account: String, result: JSONObject, stillCurrent: () -> Boolean) {
        check(stillCurrent())
        val db = writableDatabase
        db.beginTransaction()
        try {
            val removed = result.getJSONArray("removed")
            for (i in 0 until removed.length()) db.delete("photos", "account=? AND pid=?", arrayOf(account, removed.getString(i)))
            val photos = result.getJSONArray("photos")
            for (i in 0 until photos.length()) {
                check(db.insertWithOnConflict("photos", null, photoRow(account, photos.getJSONObject(i)), SQLiteDatabase.CONFLICT_REPLACE) != -1L)
            }
            saveToken(db, account, result.getString("token"))
            check(stillCurrent())
            db.setTransactionSuccessful()
        } finally { db.endTransaction() }
    }

    private fun refreshPages(account: String, token: String, stillCurrent: () -> Boolean, fetch: (String) -> JSONObject) {
        val db = writableDatabase
        val args = arrayOf(account)
        db.delete("photo_staging", "account=?", args)
        try {
            var cursor = ""
            var received = 0
            do {
                val page = fetch(cursor)
                val photos = page.getJSONArray("photos")
                val next = page.getString("nextCursor")
                received += photos.length()
                val total = page.getInt("total")
                check(if (next.isEmpty()) received == total else photos.length() > 0 && received < total && next != cursor)
                check(stillCurrent())
                db.beginTransaction()
                try {
                    for (i in 0 until photos.length()) db.insertOrThrow("photo_staging", null, photoRow(account, photos.getJSONObject(i)))
                    db.setTransactionSuccessful()
                } finally { db.endTransaction() }
                cursor = next
            } while (cursor.isNotEmpty())

            db.beginTransaction()
            try {
                check(stillCurrent())
                db.delete("photos", "account=?", args)
                db.execSQL("INSERT INTO photos SELECT * FROM photo_staging WHERE account=?", args)
                saveToken(db, account, token)
                check(stillCurrent())
                db.setTransactionSuccessful()
            } finally { db.endTransaction() }
        } finally {
            db.delete("photo_staging", "account=?", args)
        }
    }
}
