package com.filehub.backup

import android.content.res.Resources
import androidx.annotation.StringRes
import java.io.IOException

internal class LocalRequestException(
    @StringRes val title: Int, diagnostic: String, val httpCode: Int? = null,
) : IOException(diagnostic)

/** Translate local failures at the UI boundary, leaving server and system diagnostics intact. */
internal fun Resources.requestError(error: Throwable, @StringRes fallback: Int): String =
    if (error is LocalRequestException) {
        error.httpCode?.let { getString(error.title, it) } ?: getString(error.title)
    } else error.message?.takeIf { it.isNotBlank() } ?: getString(fallback)

internal fun Resources.transferError(error: Exception, fallback: Int): String {
    if (error is TransferConnectionException) return getString(R.string.transfer_ui_connection_failed)
    if (error !is UploadException) return requestError(error, fallback)
    val label = when (error.reason) {
        UploadError.CREATE_FAILED -> R.string.transfer_ui_create_failed
        UploadError.NO_UPLOAD_ID -> R.string.transfer_ui_no_upload_id
        UploadError.INVALID_ADDRESS -> R.string.transfer_ui_invalid_upload_address
        UploadError.OLD_SERVER -> R.string.transfer_ui_old_server
        UploadError.RECEIPT_FAILED -> R.string.transfer_ui_receipt_failed
        UploadError.EMPTY_RESPONSE -> R.string.transfer_ui_empty_response
        UploadError.WRONG_SOURCE -> R.string.transfer_ui_wrong_source
        UploadError.OFFSET_FAILED -> R.string.transfer_ui_offset_failed
        UploadError.NO_OFFSET -> R.string.transfer_ui_no_offset
        UploadError.FILE_CHANGED -> R.string.transfer_ui_file_changed
        UploadError.ORIGINAL_UNVERIFIED -> R.string.transfer_ui_check_original_failed
        UploadError.FILE_MISMATCH -> R.string.transfer_ui_file_mismatch
        UploadError.FILE_CHANGED_DURING_UPLOAD -> R.string.transfer_ui_file_changed_upload
        UploadError.STILL_FINALIZING -> R.string.transfer_ui_finalizing
        UploadError.OPEN_FAILED -> R.string.transfer_ui_open_file_failed
        UploadError.WRITE_FAILED -> R.string.transfer_ui_write_failed
        UploadError.NO_RECEIPT -> R.string.transfer_ui_no_receipt
        UploadError.RESUME_LATER -> R.string.transfer_ui_resume_later
        UploadError.PERSONAL_QUOTA_FULL -> R.string.personal_quota_full
    }
    return error.code?.let { getString(label, it) } ?: getString(label)
}
