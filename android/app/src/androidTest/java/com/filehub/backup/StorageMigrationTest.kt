package com.filehub.backup

import android.content.Context
import android.content.ContextWrapper
import android.database.DatabaseErrorHandler
import android.database.sqlite.SQLiteDatabase
import android.net.Uri
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import java.util.UUID

@RunWith(AndroidJUnit4::class)
class StorageMigrationTest {
    private fun context()=InstrumentationRegistry.getInstrumentation().targetContext

    @Test fun legacyPasswordMigratesAndKeystoreDecryptsAfterReopen() {
        val prefs=context().getSharedPreferences("test-vault-${UUID.randomUUID()}",Context.MODE_PRIVATE)
        assertTrue(prefs.edit().putString("pass","synthetic-emulator-fixture").commit())
        assertEquals("synthetic-emulator-fixture",PasswordVault(prefs).read())
        assertFalse(prefs.contains("pass"))
        assertTrue(prefs.contains("pass_encrypted_v1"))
        assertEquals("synthetic-emulator-fixture",PasswordVault(prefs).read())
        PasswordVault(prefs).write("synthetic-replacement")
        assertEquals("synthetic-replacement",PasswordVault(prefs).read())
        assertFalse(prefs.contains("pass"))
    }

    @Test fun tamperedEnvelopeFailsClosedAndIsPreserved() {
        val prefs=context().getSharedPreferences("test-vault-${UUID.randomUUID()}",Context.MODE_PRIVATE)
        PasswordVault(prefs).write("synthetic-emulator-fixture")
        val encrypted=prefs.getString("pass_encrypted_v1",null)!!
        val parts=encrypted.split(':')
        val changed=parts[0]+":"+(if(parts[1][0]=='A') "B" else "A")+parts[1].substring(1)
        assertTrue(prefs.edit().putString("pass_encrypted_v1",changed).commit())
        try {PasswordVault(prefs).read();fail("Tampered ciphertext must fail")}
        catch(_: java.security.GeneralSecurityException) { }
        assertEquals(changed,prefs.getString("pass_encrypted_v1",null))
    }

    private class IsolatedDatabaseContext(base: Context): ContextWrapper(base) {
        private val prefix="test-${UUID.randomUUID()}-"
        override fun getDatabasePath(name: String)=baseContext.getDatabasePath(prefix+name)
        override fun openOrCreateDatabase(name: String,mode: Int,factory: SQLiteDatabase.CursorFactory?)=
            baseContext.openOrCreateDatabase(prefix+name,mode,factory)
        override fun openOrCreateDatabase(name: String,mode: Int,factory: SQLiteDatabase.CursorFactory?,handler: DatabaseErrorHandler?)=
            baseContext.openOrCreateDatabase(prefix+name,mode,factory,handler)
    }

    @Test fun schemaThreeReceiptsSurviveMigrationInvalidationAndRecovery() {
        val ctx=IsolatedDatabaseContext(context())
        ctx.openOrCreateDatabase("backup_receipts.db",Context.MODE_PRIVATE,null).use {db->
            db.execSQL("""CREATE TABLE receipts(scope TEXT NOT NULL,media_key TEXT NOT NULL,size INTEGER NOT NULL,
                modified INTEGER NOT NULL,remote_path TEXT NOT NULL,sha256 TEXT NOT NULL,verified_at INTEGER NOT NULL,
                PRIMARY KEY(scope,media_key))""")
            db.execSQL("INSERT INTO receipts VALUES(?,?,?,?,?,?,?)",arrayOf("test-scope","i:7",42,100,"renamed/original.jpg","a".repeat(64),1))
            db.execSQL("""CREATE TABLE upload_queue(scope TEXT NOT NULL,media_key TEXT NOT NULL,size INTEGER NOT NULL,
                modified INTEGER NOT NULL,name TEXT NOT NULL,uri TEXT NOT NULL,is_video INTEGER NOT NULL,
                session_url TEXT,local_hash TEXT,state TEXT NOT NULL DEFAULT 'queued',error TEXT NOT NULL DEFAULT '',
                attempts INTEGER NOT NULL DEFAULT 0,next_try INTEGER NOT NULL DEFAULT 0,updated INTEGER NOT NULL DEFAULT 0,
                PRIMARY KEY(scope,media_key))""")
            db.version=3
        }
        BackupLedger(ctx).use {ledger->
            assertEquals(4,ledger.readableDatabase.version)
            assertEquals("renamed/original.jpg",ledger.all("test-scope").getValue("i:7").remotePath)
            assertTrue(ledger.all("test-scope").getValue("i:7").valid)
            ledger.invalidate("test-scope","i:7")
        }
        BackupLedger(ctx).use {ledger->
            val record=ledger.all("test-scope").getValue("i:7")
            val item=MediaScanner.Item(7,"original.jpg",Uri.parse("content://test/7"),false,100,42,100,"Test")
            assertFalse(record.valid)
            assertFalse(BackupLedger.matches(record,item))
            ledger.verified("test-scope","i:7")
            assertTrue(BackupLedger.matches(ledger.all("test-scope")["i:7"],item))
        }
    }
}
