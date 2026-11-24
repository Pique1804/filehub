package com.filehub.backup

import androidx.compose.runtime.mutableStateOf
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.*

/** Bounded, account-local folder cache; stale responses cannot replace another folder. */
class FileListingModel:ViewModel() {
    val sources=mutableStateOf<List<Source>>(emptyList())
    val entries=mutableStateOf<List<Entry>>(emptyList())
    val loading=mutableStateOf(false)
    val failed=mutableStateOf(false)
    val online=mutableStateOf(true)
    private data class Listing(val sources:List<Source>,val entries:List<Entry>,val time:Long)
    private val cache=LinkedHashMap<Pair<String?,String>,Listing>(16,0.75f,true)
    private var request:Job?=null
    fun load(source:String?,path:String,force:Boolean) {
        request?.cancel()
        val key=source to path
        val saved=cache[key]
        sources.value=saved?.sources.orEmpty();entries.value=saved?.entries.orEmpty();failed.value=false
        if(!force && saved!=null && android.os.SystemClock.elapsedRealtime()-saved.time<60_000){loading.value=false;return}
        val recipient=Net.server to Net.user
        val client=Net.client;val base=Net.base()
        request=viewModelScope.launch {
            loading.value=true
            val result=withContext(Dispatchers.IO) {runCatching {
                fun json(path:String)=client.newCall(okhttp3.Request.Builder().url(base+path).build()).execute().use {check(it.isSuccessful);org.json.JSONObject(it.body!!.string())}
                if(source==null) {
                    val arr=json("/api/sources").getJSONArray("sources")
                    Listing((0 until arr.length()).map {val o=arr.getJSONObject(it);Source(o.getString("name"),o.optLong("total"),o.optLong("free"))},emptyList(),android.os.SystemClock.elapsedRealtime())
                }else {
                    val src=java.net.URLEncoder.encode(source,"UTF-8");val dir=java.net.URLEncoder.encode(path,"UTF-8")
                    val arr=json("/api/list?source=$src&path=$dir&sort=name&order=asc").getJSONArray("entries")
                    Listing(emptyList(),(0 until arr.length()).map {val o=arr.getJSONObject(it);Entry(o.getString("name"),o.optBoolean("isDir"),o.optLong("size"),o.optLong("modTime"), owner = o.optString("owner"))},android.os.SystemClock.elapsedRealtime())
                }
            }}
            ensureActive()
            if((Net.server to Net.user)!=recipient)return@launch
            result.onSuccess {listing->
                cache[key]=listing
                while(cache.size>12 || cache.size>1 && cache.values.sumOf {it.entries.size}>20_000)cache.remove(cache.keys.first())
                sources.value=listing.sources;entries.value=listing.entries
            }
            failed.value=result.isFailure;online.value=result.isSuccess
            loading.value=false
        }
    }
}
