package com.filehub.backup
import android.content.Context
import kotlinx.coroutines.*
import okhttp3.*
import java.io.File
import java.security.MessageDigest

object Net {
 fun base()=System.getenv("FILEHUB_TEST_URL")
 fun url(path: String)=base()+path
 val client=OkHttpClient.Builder().addInterceptor { chain ->
  chain.proceed(chain.request().newBuilder().header("Cookie","fh_session="+System.getenv("FILEHUB_TEST_COOKIE")).build())
 }.build()
}
class Prefs { val source="T";val folder="" }
object Api { data class UploadReceipt(val path: String,val size: Long,val sha256: String="") }
object MediaScanner {
 data class Item(val id: Long,val name: String,val uri: String,val isVideo: Boolean,val size: Long,val modified: Long)
 fun unchanged(ctx: Context,item: Item)=File(item.uri).let { it.length()==item.size && it.lastModified()==item.modified }
}
val MediaScanner.Item.backupKey: String get()="v:$id"
class BackupLedger {
 data class Transfer(val url: String?,val hash: String?)
 var saved=Transfer(null,null)
 fun transfer(scope: String,item: MediaScanner.Item)=saved
 fun saveTransfer(scope: String,item: MediaScanner.Item,url: String?,hash: String?) { saved=Transfer(url,hash) }
 companion object {
  fun hashLocal(ctx: Context,item: MediaScanner.Item,check: ()->Unit): String? {
   val digest=MessageDigest.getInstance("SHA-256")
   ctx.contentResolver.openInputStream(item.uri).use { input -> val b=ByteArray(128*1024);while(true) {check();val n=input.read(b);if(n<0) break;digest.update(b,0,n)} }
   return hex(digest.digest())
  }
 }
}
fun hex(b: ByteArray)=b.joinToString("") { "%02x".format(it.toInt() and 255) }
fun main(args: Array<String>)= runBlocking { supervisorScope {
 val file=File(args[0]);val mode=args[1];val ctx=Context();val ledger=BackupLedger()
 val item=MediaScanner.Item(1,"phone.mp4",file.path,true,file.length(),file.lastModified())
 val expected=hex(MessageDigest.getInstance("SHA-256").digest(file.readBytes()))
 val self=currentCoroutineContext()
 val task=async { ResumableUploader.upload(ctx,Prefs(),item,ledger,"test",{currentCoroutineContextCheck(self)}, {_,_->},{}) }
 if(mode=="cancel") {delay(700);task.cancelAndJoin();println("PASS cancellation");return@supervisorScope}
 try {
  val result=task.await()
  check(mode!="changed") {"Changed source was confirmed"}
  check(result.size==item.size && result.sha256==expected) {"Whole-file checksum mismatch"}
  if(mode in setOf("fresh", "lost-final", "existing", "old-server")) check(ctx.contentResolver.opens==1) {"Transfer reread source ${ctx.contentResolver.opens} times"}
  check(ledger.saved.hash==expected) {"Checksum was not saved"}
  println("PASS $mode source_reads=${ctx.contentResolver.opens}")
 } catch(e: Exception) {if(mode!="changed") throw e;println("PASS changed source rejected")}
 Net.client.dispatcher.executorService.shutdown()
 Net.client.connectionPool.evictAll()
}
}
fun currentCoroutineContextCheck(ctx: kotlin.coroutines.CoroutineContext) {ctx.ensureActive()}
