package com.filehub.backup

import android.content.ContentValues
import android.content.Context
import android.database.sqlite.SQLiteDatabase
import android.database.sqlite.SQLiteOpenHelper
import java.security.MessageDigest

/** Receipts for one exact local media revision on one Filehub account/target. */
class BackupLedger(ctx: Context) : SQLiteOpenHelper(ctx, "backup_receipts.db", null, 4) {
    data class Receipt(
        val key: String, val size: Long, val modified: Long,
        val remotePath: String, val sha256: String, val verifiedAt: Long, val valid: Boolean = true,
    )

    override fun onCreate(db: SQLiteDatabase) {
        db.execSQL("""CREATE TABLE receipts (
            scope TEXT NOT NULL, media_key TEXT NOT NULL, size INTEGER NOT NULL,
            modified INTEGER NOT NULL, remote_path TEXT NOT NULL, sha256 TEXT NOT NULL,
            verified_at INTEGER NOT NULL, valid INTEGER NOT NULL DEFAULT 1, PRIMARY KEY(scope,media_key))""")
        createQueue(db)
        addRetryColumns(db)
    }
    private fun createQueue(db: SQLiteDatabase) {
        db.execSQL("""CREATE TABLE IF NOT EXISTS upload_queue (
            scope TEXT NOT NULL, media_key TEXT NOT NULL, size INTEGER NOT NULL,
            modified INTEGER NOT NULL, name TEXT NOT NULL, uri TEXT NOT NULL,
            is_video INTEGER NOT NULL, session_url TEXT, local_hash TEXT,
            state TEXT NOT NULL DEFAULT 'queued', error TEXT NOT NULL DEFAULT '',
            PRIMARY KEY(scope, media_key))""")
    }
    override fun onUpgrade(db: SQLiteDatabase, oldVersion: Int, newVersion: Int) {
        createQueue(db)
        if(oldVersion<3)addRetryColumns(db)
        if(oldVersion<4)db.execSQL("ALTER TABLE receipts ADD COLUMN valid INTEGER NOT NULL DEFAULT 1")
    }

    private fun hasColumn(db: SQLiteDatabase,name: String): Boolean = db.rawQuery("PRAGMA table_info(upload_queue)",null).use {c->
        var found=false
        while(c.moveToNext())if(c.getString(1)==name)found=true
        found
    }
    private fun addRetryColumns(db: SQLiteDatabase) {
        if(!hasColumn(db,"attempts"))db.execSQL("ALTER TABLE upload_queue ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0")
        if(!hasColumn(db,"next_try"))db.execSQL("ALTER TABLE upload_queue ADD COLUMN next_try INTEGER NOT NULL DEFAULT 0")
        if(!hasColumn(db,"updated"))db.execSQL("ALTER TABLE upload_queue ADD COLUMN updated INTEGER NOT NULL DEFAULT 0")
    }
    @Synchronized fun due(scope: String,item: MediaScanner.Item): Boolean = readableDatabase.rawQuery(
        "SELECT next_try FROM upload_queue WHERE scope=? AND media_key=?",arrayOf(scope,item.backupKey)).use { !it.moveToFirst() || it.getLong(0)<=System.currentTimeMillis() }
    @Synchronized fun nextRetry(scope: String): Long? = readableDatabase.rawQuery(
        "SELECT min(next_try) FROM upload_queue WHERE scope=? AND state='retry'",arrayOf(scope)).use {if(it.moveToFirst()&&!it.isNull(0)) it.getLong(0) else null}
    @Synchronized fun resetRetries(scope: String) {writableDatabase.execSQL("UPDATE upload_queue SET next_try=0,attempts=0 WHERE scope=? AND state='retry'",arrayOf(scope))}
    @Synchronized fun invalidate(scope: String,key: String) {
        val db=writableDatabase;db.beginTransaction()
        try{db.execSQL("UPDATE receipts SET valid=0,verified_at=? WHERE scope=? AND media_key=?",arrayOf(System.currentTimeMillis(),scope,key))
            db.execSQL("UPDATE upload_queue SET state='queued',session_url=NULL,local_hash=NULL,next_try=0,attempts=0 WHERE scope=? AND media_key=?",arrayOf(scope,key));db.setTransactionSuccessful()
        }finally{db.endTransaction()}
    }
    @Synchronized fun verified(scope: String,key: String) {writableDatabase.execSQL("UPDATE receipts SET valid=1,verified_at=? WHERE scope=? AND media_key=?",arrayOf(System.currentTimeMillis(),scope,key))}
    data class History(val name: String,val state: String,val error: String,val updated: Long,val nextTry: Long)
    @Synchronized fun history(scope: String): List<History> = readableDatabase.rawQuery(
        "SELECT name,state,error,updated,next_try FROM upload_queue WHERE scope=? ORDER BY updated DESC LIMIT 100",arrayOf(scope)).use {c->buildList{while(c.moveToNext())add(History(c.getString(0),c.getString(1),c.getString(2),c.getLong(3),c.getLong(4)))}}

    override fun onDowngrade(db: SQLiteDatabase, oldVersion: Int, newVersion: Int) = Unit

    data class Transfer(val url: String?, val hash: String?)

    @Synchronized fun enqueue(scope: String, item: MediaScanner.Item) {
        val old = readableDatabase.rawQuery("SELECT size,modified FROM upload_queue WHERE scope=? AND media_key=?",
            arrayOf(scope,item.backupKey)).use { c -> if(c.moveToFirst()) c.getLong(0) to c.getLong(1) else null }
        if (old == (item.size to item.modified)) return
        val inserted = writableDatabase.insertWithOnConflict("upload_queue", null, ContentValues().apply {
            put("scope",scope); put("media_key",item.backupKey); put("size",item.size); put("modified",item.modified)
            put("name",item.name); put("uri",item.uri.toString()); put("is_video",if(item.isVideo) 1 else 0)
            put("state","queued")
        },SQLiteDatabase.CONFLICT_REPLACE)
        check(inserted != -1L) { "Не удалось сохранить очередь" }
    }

    @Synchronized fun transfer(scope: String, item: MediaScanner.Item): Transfer =
        readableDatabase.rawQuery("SELECT session_url,local_hash FROM upload_queue WHERE scope=? AND media_key=? AND size=? AND modified=?",
            arrayOf(scope,item.backupKey,item.size.toString(),item.modified.toString())).use { c ->
            if(c.moveToFirst()) Transfer(c.getString(0),c.getString(1)) else Transfer(null,null)
        }

    @Synchronized fun saveTransfer(scope: String, item: MediaScanner.Item, url: String?, hash: String?) {
        writableDatabase.update("upload_queue",ContentValues().apply {
            if(url == null) putNull("session_url") else put("session_url",url)
            if(hash == null) putNull("local_hash") else put("local_hash",hash)
        },"scope=? AND media_key=? AND size=? AND modified=?",arrayOf(scope,item.backupKey,item.size.toString(),item.modified.toString()))
    }

    @Synchronized fun state(scope: String, item: MediaScanner.Item, state: String, error: String = "") {
        writableDatabase.update("upload_queue",ContentValues().apply { put("state",state); put("error",error); put("updated",System.currentTimeMillis()) },
            "scope=? AND media_key=?",arrayOf(scope,item.backupKey))
    }

    @Synchronized fun failed(scope: String,item: MediaScanner.Item,error: String) {
        val attempts=readableDatabase.rawQuery("SELECT attempts FROM upload_queue WHERE scope=? AND media_key=?",arrayOf(scope,item.backupKey)).use {if(it.moveToFirst())it.getInt(0)+1 else 1}
        state(scope,item,"retry",error)
        writableDatabase.execSQL("UPDATE upload_queue SET attempts=?,next_try=? WHERE scope=? AND media_key=?",
            arrayOf(attempts,System.currentTimeMillis()+minOf(6*60*60*1000L,30000L*(1L shl attempts.coerceAtMost(10))),scope,item.backupKey))
    }

    @Synchronized fun all(scope: String): Map<String, Receipt> {
        val result = HashMap<String, Receipt>()
        readableDatabase.query("receipts", arrayOf("media_key", "size", "modified", "remote_path", "sha256", "verified_at", "valid"),
            "scope=?", arrayOf(scope), null, null, null).use { c ->
            while (c.moveToNext()) {
                val r = Receipt(c.getString(0), c.getLong(1), c.getLong(2), c.getString(3), c.getString(4), c.getLong(5), c.getInt(6)==1)
                result[r.key] = r
            }
        }
        return result
    }

    @Synchronized fun put(scope: String, item: MediaScanner.Item, remotePath: String, hash: String) {
        val values = ContentValues().apply {
            put("scope", scope); put("media_key", item.backupKey)
            put("size", item.size); put("modified", item.modified)
            put("remote_path", remotePath); put("sha256", hash)
            put("verified_at", System.currentTimeMillis())
        }
        val db = writableDatabase
        db.beginTransaction()
        try {
            check(db.insertWithOnConflict("receipts", null, values, SQLiteDatabase.CONFLICT_REPLACE) != -1L) { "Не удалось сохранить подтверждение" }
            state(scope,item,"complete")
            db.setTransactionSuccessful()
        } finally { db.endTransaction() }
    }

    companion object {
        fun matchesRevision(r: Receipt?, item: MediaScanner.Item) =
            r != null && item.size > 0 && r.size == item.size && r.modified == item.modified && r.sha256.length == 64

        fun matches(r: Receipt?, item: MediaScanner.Item) =
            r?.valid == true && matchesRevision(r, item)

        fun hashLocal(ctx: Context, item: MediaScanner.Item, checkActive: () -> Unit = {}): String? { return try {
            val digest = MessageDigest.getInstance("SHA-256")
            val input = ctx.contentResolver.openInputStream(item.uri) ?: return null
            var count = 0L
            input.use { stream ->
                val buffer = ByteArray(128 * 1024)
                while (true) {
                    checkActive()
                    val n = stream.read(buffer)
                    if (n < 0) break
                    if (n > 0) { digest.update(buffer, 0, n); count += n }
                }
            }
            if (count != item.size || count <= 0) null
            else digest.digest().joinToString("") { "%02x".format(it) }
        } catch (e: kotlinx.coroutines.CancellationException) { throw e } catch (e: Exception) { null } }
    }
}

val MediaScanner.Item.backupKey: String get() = (if(album=="__shared__") "s:" else if (isVideo) "v:" else "i:") + id
