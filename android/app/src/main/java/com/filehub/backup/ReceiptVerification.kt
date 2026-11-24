package com.filehub.backup

import android.content.res.Resources

/** A failed request or an omitted answer is never evidence that a copy is absent. */
data class ReceiptCheck(val valid: Boolean, val verified: Boolean = true)

object ReceiptVerification {
    const val MAX_PER_RUN = 1000
    const val BATCH_SIZE = 20
    const val MAX_AGE_MS = 24 * 60 * 60 * 1000L

    data class Result(val checked: Int, val invalid: Int, val pending: Int)

    /** Persist each answer immediately; unfinished keys remain due after process death. */
    suspend fun <T> run(
        stale: List<T>, key: (T) -> String,
        request: suspend (List<T>) -> Map<String, ReceiptCheck>?,
        persist: (T, Boolean) -> Unit,
        progress: (Int, Int) -> Unit = { _, _ -> },
    ): Result {
        var checked = 0
        var invalid = 0
        for (batch in stale.take(MAX_PER_RUN).chunked(BATCH_SIZE)) {
            progress(checked, stale.size)
            val answers = request(batch) ?: break
            var incomplete = false
            for (item in batch) {
                val answer = answers[key(item)]
                if (answer == null || !answer.verified) { incomplete = true; continue }
                persist(item, answer.valid)
                checked++
                if (!answer.valid) invalid++
            }
            // Do not hammer an unavailable server. WorkManager retries the durable remainder.
            if (incomplete) break
        }
        return Result(checked, invalid, stale.size - checked)
    }

    internal data class Summary(
        val uploaded: Int, val uploadPending: Boolean, val verificationPending: Int, val invalid: Int,
    ) {
        val uploadLabel: Int get() =
            if (uploadPending) R.string.transfer_ui_summary_with_pending else R.string.transfer_ui_summary_uploaded
        val verificationLabel: Int get() = when {
            verificationPending > 0 -> R.string.transfer_ui_summary_verification_pending
            invalid > 0 -> R.string.transfer_ui_summary_verification_invalid
            else -> R.string.transfer_ui_summary_verified
        }

        fun text(resources: Resources): String {
            val upload = if (uploadPending) resources.getString(uploadLabel, uploaded)
                else resources.getString(uploadLabel)
            val verification = when {
                verificationPending > 0 -> resources.getString(verificationLabel, verificationPending)
                invalid > 0 -> resources.getString(verificationLabel, invalid)
                else -> resources.getString(verificationLabel)
            }
            return resources.getString(R.string.transfer_ui_summary, upload, verification)
        }
    }
}
