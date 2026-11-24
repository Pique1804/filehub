package com.filehub.backup

/** Keystore and preferences are adapters, so migration failure can be tested without a device. */
internal class CredentialMigration(private val storage: Storage, private val cipher: Cipher) {
    interface Storage {
        fun encrypted(): String?
        fun legacy(): String?
        /** Must atomically persist ciphertext and remove plaintext, or throw. */
        fun replace(encrypted: String)
    }
    interface Cipher {
        fun encrypt(plain: String): String
        fun decrypt(encrypted: String): String
    }

    fun read(): String {
        storage.encrypted()?.let { return cipher.decrypt(it) }
        val legacy=storage.legacy() ?: return ""
        // A transient Keystore/storage failure must not destroy a previously valid login.
        try { write(legacy) } catch (_: Exception) { }
        return legacy
    }

    fun write(plain: String) {
        val encrypted=cipher.encrypt(plain)
        check(cipher.decrypt(encrypted)==plain) { "Не удалось проверить защищённое хранилище" }
        storage.replace(encrypted)
    }
}
