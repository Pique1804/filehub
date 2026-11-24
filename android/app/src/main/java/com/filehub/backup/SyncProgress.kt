package com.filehub.backup

import android.content.res.Resources
import androidx.work.Data
import androidx.work.workDataOf
import java.text.NumberFormat

internal enum class TransferPhase(private val label: Int) {
    PREPARING(R.string.backup_queue_preparing),
    RECONCILING(R.string.transfer_ui_phase_checking_copies),
    TRANSFERRING(R.string.transfer_ui_phase_transferring),
    SAVING(R.string.transfer_ui_phase_saving),
    VERIFYING(R.string.transfer_ui_phase_checking_file),
    RECONNECTING(R.string.transfer_ui_phase_reconnecting),
    RETRY_WAIT(R.string.transfer_ui_phase_retry_wait),
    DONE(R.string.transfer_ui_phase_done);

    fun text(resources: Resources, copyCheckDone: Int, copyCheckTotal: Int): String =
        if (this == RECONCILING) resources.getString(label, copyCheckDone, copyCheckTotal)
        else resources.getString(label)

    companion object {
        fun fromCode(code: String) = entries.firstOrNull { it.name == code }
        fun fromUpload(phase: UploadPhase) = when (phase) {
            UploadPhase.TRANSFERRING -> TRANSFERRING
            UploadPhase.VERIFYING -> VERIFYING
            UploadPhase.RECONNECTING -> RECONNECTING
        }
    }
}

internal data class ActiveTransfer(val name: String, val percent: Int, val phase: TransferPhase) {
    fun text(resources: Resources): String =
        resources.getString(R.string.transfer_ui_active_file, name, percent, phase.text(resources, 0, 0))
}

/** Byte progress is separate from the number of independently verified copies. */
internal data class SyncProgress(
    val done: Int = 0,
    val total: Int = 0,
    val bytes: Long = 0,
    val totalBytes: Long = 0,
    val bytesPerSecond: Long = 0,
    val phase: String = "",
    val activeFiles: List<String> = emptyList(),
    val idleSeconds: Long = 0,
    val phaseCodes: List<TransferPhase> = emptyList(),
    val transfers: List<ActiveTransfer> = emptyList(),
    val copyCheckDone: Int = 0,
    val copyCheckTotal: Int = 0,
) {
    val fraction: Float get() = if (totalBytes > 0)
        (bytes.toDouble() / totalBytes).coerceIn(0.0, 1.0).toFloat() else 0f
    // Keep all byte arithmetic in Long/Double: videos can exceed Int.MAX_VALUE.
    val barValue: Int get() = (fraction * 1000).toInt()
    val percent: Int get() = (fraction * 100).toInt()

    private fun phaseLabel(resources: Resources): String =
        if (phaseCodes.isNotEmpty()) phaseCodes.joinToString(" · ") { it.text(resources, copyCheckDone, copyCheckTotal) }
        else phase.ifBlank { resources.getString(R.string.backup_queue_preparing) }

    fun activity(resources: Resources): String {
        // Earlier app versions persisted only the Russian display text.
        val reconnecting = TransferPhase.RECONNECTING in phaseCodes ||
            (phaseCodes.isEmpty() && phase.contains("Восстановление связи"))
        val transferring = TransferPhase.TRANSFERRING in phaseCodes ||
            (phaseCodes.isEmpty() && phase.split(" · ").contains("Передача"))
        return if (!reconnecting && transferring && idleSeconds >= 10)
            resources.getString(R.string.transfer_ui_idle, idleSeconds) else phaseLabel(resources)
    }

    fun activeFileLabels(resources: Resources): List<String> =
        if (transfers.isEmpty()) activeFiles else transfers.map { it.text(resources) }

    fun workData(resources: Resources, failed: Int = 0): Data = workDataOf(
        "done" to done, "total" to total, "bytes" to bytes, "totalBytes" to totalBytes,
        "bytesPerSecond" to bytesPerSecond, "idleSeconds" to idleSeconds, "failed" to failed,
        // Preserve the display fields for readers of earlier progress snapshots.
        "phase" to phaseLabel(resources), "activeFiles" to activeFileLabels(resources).toTypedArray(),
        "phaseCodes" to phaseCodes.map { it.name }.toTypedArray(),
        "fileNames" to transfers.map { it.name }.toTypedArray(),
        "filePercentages" to transfers.map { it.percent }.toIntArray(),
        "filePhaseCodes" to transfers.map { it.phase.name }.toTypedArray(),
        "copyCheckDone" to copyCheckDone, "copyCheckTotal" to copyCheckTotal,
    )

    companion object {
        fun from(data: Data): SyncProgress {
            val names = data.getStringArray("fileNames").orEmpty()
            val percentages = data.getIntArray("filePercentages") ?: intArrayOf()
            val filePhases = data.getStringArray("filePhaseCodes").orEmpty()
            val active = if (names.size == percentages.size && names.size == filePhases.size)
                names.indices.mapNotNull { index ->
                    TransferPhase.fromCode(filePhases[index])?.let { ActiveTransfer(names[index], percentages[index], it) }
                }
            else emptyList()
            return SyncProgress(
                done = data.getInt("done", 0), total = data.getInt("total", 0),
                bytes = data.getLong("bytes", 0), totalBytes = data.getLong("totalBytes", 0),
                bytesPerSecond = data.getLong("bytesPerSecond", 0),
                phase = data.getString("phase").orEmpty(),
                activeFiles = data.getStringArray("activeFiles").orEmpty().toList(),
                idleSeconds = data.getLong("idleSeconds", 0),
                phaseCodes = data.getStringArray("phaseCodes").orEmpty().mapNotNull { TransferPhase.fromCode(it) },
                transfers = active,
                copyCheckDone = data.getInt("copyCheckDone", 0), copyCheckTotal = data.getInt("copyCheckTotal", 0),
            )
        }
    }

    fun byteLabel(resources: Resources): String {
        val rate = NumberFormat.getNumberInstance(resources.configuration.locales[0]).apply {
            minimumFractionDigits = 1
            maximumFractionDigits = 1
        }.format(bytesPerSecond / 1048576.0)
        return resources.getString(
            R.string.backup_sync_transfer_rate,
            localizedSize(resources, bytes), localizedSize(resources, totalBytes), rate,
        )
    }

    fun verifiedLabel(resources: Resources): String =
        resources.getString(R.string.backup_sync_verified_count, done, total)
}

/** Use binary file units so the displayed value agrees with the transfer byte counter. */
internal fun localizedSize(resources: Resources, bytes: Long): String {
    val (divisor, unit) = when {
        bytes >= 1073741824L -> 1073741824L to R.string.file_size_gib
        bytes >= 1048576L -> 1048576L to R.string.file_size_mib
        bytes >= 1024L -> 1024L to R.string.file_size_kib
        else -> 1L to R.string.file_size_bytes
    }
    val value = NumberFormat.getNumberInstance(resources.configuration.locales[0]).apply {
        maximumFractionDigits = if (divisor == 1L) 0 else 1
    }.format(bytes.toDouble() / divisor)
    return resources.getString(unit, value)
}
