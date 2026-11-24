package com.filehub.backup

import android.content.SharedPreferences
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/** AES-GCM password envelope; the non-exportable encryption key stays in Android Keystore. */
internal class PasswordVault(private val preferences: SharedPreferences) {
    companion object {
        private val lock=Any()
        private const val ALIAS="filehub.password.v1"
        private const val ENCRYPTED="pass_encrypted_v1"
        private val AAD="com.filehub.backup/password/v1".toByteArray(Charsets.UTF_8)
    }

    private fun key(create: Boolean): SecretKey {
        val store=KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (store.getKey(ALIAS,null) as? SecretKey)?.let { return it }
        check(create) { "Защищённый пароль недоступен; войдите снова" }
        return KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES,"AndroidKeyStore").apply {
            init(KeyGenParameterSpec.Builder(ALIAS,KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM).setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256).setRandomizedEncryptionRequired(true).build())
        }.generateKey()
    }

    private val migration=CredentialMigration(object: CredentialMigration.Storage {
        override fun encrypted()=preferences.getString(ENCRYPTED,null)
        override fun legacy()=preferences.getString("pass",null)
        override fun replace(encrypted: String) {
            check(preferences.edit().putString(ENCRYPTED,encrypted).remove("pass").commit()) {
                "Не удалось сохранить защищённый пароль"
            }
        }
    },object: CredentialMigration.Cipher {
        override fun encrypt(plain: String): String {
            val cipher=Cipher.getInstance("AES/GCM/NoPadding")
            cipher.init(Cipher.ENCRYPT_MODE,key(create=true)); cipher.updateAAD(AAD)
            val payload=cipher.doFinal(plain.toByteArray(Charsets.UTF_8))
            return Base64.encodeToString(cipher.iv,Base64.NO_WRAP)+":"+Base64.encodeToString(payload,Base64.NO_WRAP)
        }
        override fun decrypt(encrypted: String): String {
            val parts=encrypted.split(':'); require(parts.size==2)
            val iv=Base64.decode(parts[0],Base64.NO_WRAP); require(iv.size==12)
            val cipher=Cipher.getInstance("AES/GCM/NoPadding")
            cipher.init(Cipher.DECRYPT_MODE,key(create=false),GCMParameterSpec(128,iv)); cipher.updateAAD(AAD)
            return String(cipher.doFinal(Base64.decode(parts[1],Base64.NO_WRAP)),Charsets.UTF_8)
        }
    })

    fun read(): String=synchronized(lock) { migration.read() }
    fun write(plain: String)=synchronized(lock) { migration.write(plain) }
    fun clear()=synchronized(lock) {
        check(preferences.edit().remove(ENCRYPTED).remove("pass").commit()) { "Не удалось очистить пароль" }
    }
}
