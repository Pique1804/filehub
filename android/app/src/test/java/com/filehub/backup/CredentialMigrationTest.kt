package com.filehub.backup

import org.junit.Assert.*
import org.junit.Test

class CredentialMigrationTest {
    private class Storage: CredentialMigration.Storage {
        var encrypted: String?=null
        var legacy: String?="synthetic-test-password"
        var failWrites=false
        override fun encrypted()=encrypted
        override fun legacy()=legacy
        override fun replace(encrypted: String) {
            check(!failWrites)
            this.encrypted=encrypted; legacy=null
        }
    }
    private class Cipher: CredentialMigration.Cipher {
        var fail=false
        var corrupt=false
        override fun encrypt(plain: String): String { check(!fail); return plain.reversed() }
        override fun decrypt(encrypted: String): String { check(!fail); return if(corrupt) "wrong" else encrypted.reversed() }
    }
    @Test fun successfulMigrationRemovesPlaintextAndSurvivesRecreation() {
        val storage=Storage(); val cipher=Cipher()
        assertEquals("synthetic-test-password",CredentialMigration(storage,cipher).read())
        assertNull(storage.legacy); assertNotNull(storage.encrypted)
        assertEquals("synthetic-test-password",CredentialMigration(storage,cipher).read())
    }
    @Test fun unavailableKeystoreKeepsLegacyCredentialsAndRetriesNextRead() {
        val storage=Storage(); val cipher=Cipher().apply { fail=true }
        assertEquals("synthetic-test-password",CredentialMigration(storage,cipher).read())
        assertNotNull(storage.legacy); assertNull(storage.encrypted)
        cipher.fail=false
        assertEquals("synthetic-test-password",CredentialMigration(storage,cipher).read())
        assertNull(storage.legacy)
    }
    @Test fun failedCommitKeepsOriginalPassword() {
        val storage=Storage().apply {failWrites=true}
        assertEquals("synthetic-test-password",CredentialMigration(storage,Cipher()).read())
        assertNotNull(storage.legacy); assertNull(storage.encrypted)
    }
    @Test fun failedRoundTripNeverCommitsOrClearsLegacy() {
        val storage=Storage(); val cipher=Cipher().apply {corrupt=true}
        assertEquals("synthetic-test-password",CredentialMigration(storage,cipher).read())
        assertNotNull(storage.legacy); assertNull(storage.encrypted)
    }
    @Test fun encryptedReadFailurePreservesCiphertextAndDoesNotFallback() {
        val storage=Storage().apply {encrypted="protected-existing"}
        try {CredentialMigration(storage,Cipher().apply {fail=true}).read();fail("Expected failure")}
        catch(_: IllegalStateException) { }
        assertEquals("protected-existing",storage.encrypted)
    }
    @Test fun failedNewPasswordWriteDoesNotChangeExistingEntry() {
        val storage=Storage().apply {encrypted="existing";legacy=null;failWrites=true}
        try {CredentialMigration(storage,Cipher()).write("replacement");fail("Expected failure")}
        catch(_: IllegalStateException) { }
        assertEquals("existing",storage.encrypted)
    }
}
