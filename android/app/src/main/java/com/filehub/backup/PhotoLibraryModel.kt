package com.filehub.backup

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import androidx.paging.*
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.*
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import java.util.concurrent.CopyOnWriteArrayList

sealed interface TimelineRow {
    data class Header(val section:PhotoTimeline.Section):TimelineRow
    data class Image(val photo:Photo):TimelineRow
}

/** Placeholder counts include date headers, so a jump to any date needs only one page. */
class TimelinePagingSource(
    private val timeline:PhotoTimeline,
    private val read:(Int,Int)->List<Photo>
):PagingSource<Int,TimelineRow>() {
    override val jumpingSupported=true
    override fun getRefreshKey(state:PagingState<Int,TimelineRow>):Int? = state.anchorPosition?.let {(it-state.config.initialLoadSize/2).coerceAtLeast(0)}
    override suspend fun load(params:LoadParams<Int>):LoadResult<Int,TimelineRow> = withContext(Dispatchers.IO) {
        try {
            val end=if(params is LoadParams.Prepend) (params.key ?: 0).coerceAtMost(timeline.itemCount) else ((params.key ?: 0)+params.loadSize).coerceAtMost(timeline.itemCount)
            val start=if(params is LoadParams.Prepend) (end-params.loadSize).coerceAtLeast(0) else (params.key ?: 0).coerceIn(0,end)
            val offset=timeline.photoOffset(start)
            val photos=read(offset,timeline.photoOffset(end)-offset).iterator()
            val rows=(start until end).map { i -> timeline.headerAt(i)?.let {TimelineRow.Header(it)} ?: TimelineRow.Image(photos.next()) }
            LoadResult.Page(rows,if(start==0)null else start,if(end==timeline.itemCount)null else end,itemsBefore=start,itemsAfter=timeline.itemCount-end)
        }catch(e:CancellationException){throw e}catch(e:Exception){LoadResult.Error(e)}
    }
}

data class GalleryPage(val timeline:PhotoTimeline=PhotoTimeline(),val pages:Flow<PagingData<TimelineRow>> = flowOf(PagingData.empty()))

/** One owner per signed-in account; switching tabs neither scans nor downloads the catalog again. */
class PhotoLibraryModel(app:Application):AndroidViewModel(app) {
    private val prefs=Prefs(app)
    val account=prefs.accountScope()
    private val cache=PhotoCache(app)
    private val refreshing=Mutex()
    private val favoriteUpdates=Mutex()
    private var started=false
    private var backupVersion:String?=null
    private var months=false
    private var favoritesOnly=false
    private var generationJob:Job?=null
    private var indexJob:Job?=null
    private val sources=CopyOnWriteArrayList<TimelinePagingSource>()
    private val _gallery=MutableStateFlow(GalleryPage())
    val gallery=_gallery.asStateFlow()
    val loading=MutableStateFlow(false)
    val counts=MutableStateFlow(0 to 0)
    val favorites=MutableStateFlow<Set<String>>(emptySet())
    val memories=MutableStateFlow<List<Photo>>(emptyList())
    val offline=MutableStateFlow(false)
    private fun current()=Prefs(getApplication()).accountScope()==account && Net.server.isNotEmpty()
    fun start() {
        if(started)return
        started=true
        viewModelScope.launch {
            favorites.value=withContext(Dispatchers.IO){cache.favorites(account)}
            counts.value=withContext(Dispatchers.IO){cache.counts(account)}
            rebuild()
            refresh()
        }
    }
    fun backupChanged(infos:List<androidx.work.WorkInfo>):Boolean {
        if(infos.isEmpty())return false
        val version=infos.filter {it.state.isFinished}.map {"${it.id}:${it.state}"}.sorted().joinToString()
        val previous=backupVersion;backupVersion=version
        if(previous==null || previous==version)return false
        refresh();return true
    }
    fun filter(monthly:Boolean,onlyFavorites:Boolean) {
        if(months==monthly && favoritesOnly==onlyFavorites)return
        months=monthly;favoritesOnly=onlyFavorites;rebuild()
    }
    private fun rebuild() {
        indexJob?.cancel()
        val month=months;val fav=favoritesOnly
        indexJob=viewModelScope.launch {
            val index=withContext(Dispatchers.IO){cache.timeline(account,month,fav)}
            ensureActive()
            generationJob?.cancel()
            sources.forEach {it.invalidate()};sources.clear()
            val job=SupervisorJob(viewModelScope.coroutineContext[Job]);generationJob=job
            val pages=Pager(PagingConfig(pageSize=240,initialLoadSize=240,prefetchDistance=60,maxSize=960,enablePlaceholders=true,jumpThreshold=480)) {
                TimelinePagingSource(index) {offset,count->cache.page(account,offset,count,fav)}.also {source->sources.add(source);source.registerInvalidatedCallback {sources.remove(source)}}
            }.flow.cachedIn(CoroutineScope(viewModelScope.coroutineContext+job))
            _gallery.value=GalleryPage(index,pages)
        }
    }
    fun refresh() {viewModelScope.launch {
        if(!refreshing.tryLock())return@launch
        loading.value=true
        try {
            check(current())
            val changed=withContext(Dispatchers.IO){cache.refresh(account,::current)}
            if(!current())return@launch
            offline.value=false
            if(changed){counts.value=withContext(Dispatchers.IO){cache.counts(account)};rebuild()}
            // Unlike favList's legacy empty-on-error fallback, preserve offline favorites.
            favoriteUpdates.withLock {
            val saved=withContext(Dispatchers.IO) {runCatching {val arr=Api.jsonGet("/api/fav/list").getJSONArray("items");buildSet {for(i in 0 until arr.length()){val o=arr.getJSONObject(i);add(o.getString("source")+"|"+o.getString("path"))}}}}
            if(current())saved.onSuccess {ids->if(ids!=favorites.value){favorites.value=ids;withContext(Dispatchers.IO){cache.saveFavorites(account,ids)};if(favoritesOnly)rebuild()}}
            }
            val days=withContext(Dispatchers.IO){Api.onThisDay()}
            if(current())memories.value=days
        }catch(e:CancellationException){throw e}catch(_:Exception){offline.value=true}
        finally{loading.value=false;refreshing.unlock()}
    }}
    fun toggleFavorite(photo:Photo) {viewModelScope.launch {favoriteUpdates.withLock {
        if(!current())return@withLock
        val id=photo.source+"|"+photo.path
        val before=favorites.value
        val next=if(id in before)before-id else before+id
        favorites.value=next
        val saved=withContext(Dispatchers.IO){Api.favToggle(photo.source,photo.path)}
        if(!current())return@withLock
        if(!saved)favorites.value=before
        withContext(Dispatchers.IO){cache.saveFavorites(account,favorites.value)}
        if(favoritesOnly)rebuild()
    }}}
    suspend fun all()=withContext(Dispatchers.IO){cache.load(account,favoritesOnly)}
    suspend fun ids()=withContext(Dispatchers.IO){cache.ids(account,favoritesOnly)}
    suspend fun selected(ids:Set<String>)=withContext(Dispatchers.IO){cache.selected(account,ids)}
    override fun onCleared(){generationJob?.cancel();sources.forEach {it.invalidate()};cache.close()}
}
