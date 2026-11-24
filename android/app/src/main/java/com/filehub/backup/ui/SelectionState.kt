package com.filehub.backup.ui

import androidx.compose.runtime.Composable
import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue

/** Immich-style selection: "selection mode" is derived from the set being non-empty. */
@Stable
class SelectionState {
    var ids by mutableStateOf<Set<String>>(emptySet())
        private set
    val inSelection: Boolean get() = ids.isNotEmpty()
    val count: Int get() = ids.size

    fun toggle(id: String) { ids = if (id in ids) ids - id else ids + id }
    fun selectAll(all: Collection<String>) { ids = ids + all }
    fun clear() { ids = emptySet() }
}

@Composable
fun rememberSelection() = remember { SelectionState() }
