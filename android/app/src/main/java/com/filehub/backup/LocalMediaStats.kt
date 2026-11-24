package com.filehub.backup

import android.app.Application
import android.database.ContentObserver
import android.os.Handler
import android.os.Looper
import android.provider.MediaStore
import androidx.core.content.ContextCompat
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.MutableStateFlow

/** Counters share one scan; deletion still uses FreeSpace's independent receipt/hash verification. */
class LocalMediaStats(app:Application):AndroidViewModel(app) {
    val counts=MutableStateFlow(-1 to -1)
    private var dirty=true
    private var input=""
    private var job:Job?=null
    private var visible=false
    private val observer=object:ContentObserver(Handler(Looper.getMainLooper())) {
        override fun onChange(selfChange:Boolean){dirty=true;if(visible)refresh()}
    }
    init {
        runCatching {app.contentResolver.registerContentObserver(MediaStore.Images.Media.EXTERNAL_CONTENT_URI,true,observer)}
        runCatching {app.contentResolver.registerContentObserver(MediaStore.Video.Media.EXTERNAL_CONTENT_URI,true,observer)}
    }
    fun visible(value:Boolean){visible=value;if(value)refresh()}
    fun refresh(force:Boolean=false) {
        val app=getApplication<Application>();val p=Prefs(app)
        val key=p.backupScope()+p.selectedAlbums.sorted().joinToString("\u001f")+listOf("android.permission.READ_MEDIA_IMAGES","android.permission.READ_MEDIA_VIDEO","android.permission.READ_MEDIA_VISUAL_USER_SELECTED","android.permission.READ_EXTERNAL_STORAGE").map {ContextCompat.checkSelfPermission(app,it)}
        if(key!=input){input=key;dirty=true}
        if(force)dirty=true
        if(!dirty || job?.isActive==true)return
        job=viewModelScope.launch {
            delay(350) // MediaStore often reports several changes for one capture.
            while(dirty) {
                dirty=false
                val scanInput=input
                val scope=p.backupScope();val albums=p.selectedAlbums
                val result=withContext(Dispatchers.IO) {
                    runCatching {
                        val items=MediaScanner.scan(app,albums,chronological=false)
                        val ledger=BackupLedger(app)
                        val backed=try {val records=ledger.all(scope);items.count {BackupLedger.matches(records[it.backupKey],it)}} finally {ledger.close()}
                        items.size to backed
                    }.getOrDefault(-1 to -1)
                }
                if(input==scanInput && p.backupScope()==scope && p.selectedAlbums==albums)counts.value=result else dirty=true
                if(!visible)break
            }
        }
    }
    override fun onCleared(){getApplication<Application>().contentResolver.unregisterContentObserver(observer)}
}
