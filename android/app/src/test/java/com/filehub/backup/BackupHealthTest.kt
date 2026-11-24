package com.filehub.backup

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class BackupHealthTest {
    @Test fun legacyStatusIncludesLastSuccessButNoFileEvidence() {
        val health = BackupHealth.from(JSONObject("""{"state":"error","lastSuccessfulCompleted":123,"stale":true}"""))
        assertEquals(123L, health.lastSuccessfulCheck)
        assertFalse(health.fileChecks)
        assertEquals(R.string.backup_health_error, health.statusMessage)
    }

    @Test fun managedStatusStaysDistinctFromOlderSuccessfulLegacyJob() {
        val health = BackupHealth.from(JSONObject("""{"state":"ok","completed":123,
          "managedCopy":{"state":"ok","completed":200,"stale":true,"fileChecks":true}}"""))
        assertEquals(200L, health.lastSuccessfulCheck)
        assertTrue(health.fileChecks)
        assertEquals(R.string.backup_health_stale, health.statusMessage)
        assertEquals(R.string.backup_health_unknown, BackupHealth().statusMessage)
    }

    @Test fun onlyExplicitFileConfirmationIsPositive() {
        val parsed = Api.parseBackupCopies(JSONObject("""{"items":[
          {"key":"a","state":"confirmed"},{"key":"b","state":"same_volume"},{"key":"c","valid":true},
          {"key":"d","state":"unavailable"},{"key":"e","state":"ok"}]}"""))
        assertEquals(BackupCopyState.Confirmed, parsed["a"])
        assertEquals(BackupCopyState.SameVolume, parsed["b"])
        assertEquals(BackupCopyState.Unknown, parsed["c"])
        assertEquals(BackupCopyState.Unavailable, parsed["d"])
        assertEquals(BackupCopyState.Unknown, parsed["e"])
    }
}
