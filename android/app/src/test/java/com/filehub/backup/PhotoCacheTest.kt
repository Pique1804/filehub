package com.filehub.backup

import android.app.Application
import android.content.Context
import android.database.sqlite.SQLiteDatabase
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.*
import org.junit.Assert.*
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(manifest=Config.NONE,application=Application::class,sdk=[28])
class PhotoCacheTest {
    private val context:Context get()=RuntimeEnvironment.getApplication()
    private val row="""{"name":"old.jpg","path":"Camera/old.jpg","source":"Disk","modTime":100,"isVideo":false,"revision":"r1","size":123} """.trim()
    @Before fun setup(){context.deleteDatabase("photo_cache.db")}
    @After fun cleanup(){Net.clear();context.deleteDatabase("photo_cache.db")}
    @Test fun versionOneUpgradePreservesOfflineRowsAndSnapshot() {
        context.openOrCreateDatabase("photo_cache.db",Context.MODE_PRIVATE,null).use {db->
            db.execSQL("CREATE TABLE photos(account TEXT,pid TEXT,json TEXT,PRIMARY KEY(account,pid))")
            db.execSQL("CREATE TABLE snapshots(account TEXT PRIMARY KEY,token TEXT)")
            db.execSQL("INSERT INTO photos VALUES(?,?,?)",arrayOf("owner","Disk|Camera/old.jpg",row))
            db.execSQL("INSERT INTO snapshots VALUES('owner','before')")
            db.version=1
        }
        PhotoCache(context).use {cache->
            assertEquals(1,cache.page("owner",0,240).size)
            assertEquals("r1",cache.load("owner").single().revision)
            assertEquals(123L,cache.load("owner").single().size)
            assertEquals(1 to 0,cache.counts("owner"))
            assertTrue(cache.load("other").isEmpty())
            cache.readableDatabase.rawQuery("SELECT token FROM snapshots WHERE account='owner'",null).use {it.moveToFirst();assertEquals("before",it.getString(0))}
            cache.saveFavorites("owner",setOf("Disk|Camera/old.jpg"))
            assertEquals(1,cache.page("owner",0,10,true).size)
            assertTrue(cache.page("other",0,10,true).isEmpty())
        }
    }
    @Test fun unchangedTokenAvoidsDeltaAndMalformedDeltaKeepsLastSnapshot() {
        val server=MockWebServer();server.start()
        try {
            server.enqueue(MockResponse().setHeader("Set-Cookie","fh_session=synthetic; Path=/"))
            assertTrue(Net.login(server.url("/").toString(),"test","synthetic-password",true))
            PhotoCache(context).use {cache->
                server.enqueue(MockResponse().setBody("""{"token":"one"}"""))
                server.enqueue(MockResponse().setBody("""{"token":"one","removed":[],"photos":[$row]}"""))
                assertTrue(cache.refresh("owner"))
                server.enqueue(MockResponse().setBody("""{"token":"one"}"""))
                assertFalse(cache.refresh("owner"));assertEquals(4,server.requestCount)
                server.enqueue(MockResponse().setBody("""{"token":"two"}"""))
                server.enqueue(MockResponse().setBody("""{"token":"two","removed":["Disk|Camera/old.jpg"],"photos":[{"invalid":true}]}"""))
                assertTrue(runCatching {cache.refresh("owner")}.isFailure)
                assertEquals("old.jpg",cache.load("owner").single().name)
                assertEquals(listOf("Disk|Camera/old.jpg"),cache.ids("owner"))
                assertEquals(cache.load("owner"),cache.selected("owner",setOf("Disk|Camera/old.jpg")))
            }
        }finally{server.shutdown()}
    }

    @Test fun interruptedPagesLeaveThePreviousSnapshotUntilAllPagesArrive() {
        MockWebServer().use { server ->
            server.start()
            fun reply(json: String) = server.enqueue(MockResponse().setBody(json))
            server.enqueue(MockResponse().setHeader("Set-Cookie", "fh_session=synthetic; Path=/"))
            assertTrue(Net.login(server.url("/").toString(), "test", "synthetic-password", true))
            PhotoCache(context).use { cache ->
                for (account in listOf("owner", "other")) {
                    reply("""{"token":"original"}""")
                    reply("""{"token":"original","photos":[$row],"removed":[]}""")
                    assertTrue(cache.refresh(account))
                }
                val first = row.replace("old.jpg", "new.jpg")
                val second = row.replace("old.jpg", "second.jpg")
                val firstPage = """{"photos":[$first],"total":2,"nextCursor":"page/+="}"""
                fun startPages() {
                    reply("""{"token":"next","paged":true}""")
                    reply("""{"snapshotRequired":true}""")
                    reply(firstPage)
                }
                startPages()
                server.enqueue(MockResponse().setResponseCode(409))
                assertTrue(runCatching { cache.refresh("owner") }.isFailure)
                assertEquals(listOf("old.jpg"), cache.load("owner").map { it.name })

                startPages()
                val requestsBefore = server.requestCount
                assertTrue(runCatching {
                    cache.refresh("owner") { server.requestCount < requestsBefore + 3 }
                }.isFailure)
                assertEquals(listOf("old.jpg"), cache.load("owner").map { it.name })

                startPages()
                reply("""{"photos":[$second],"total":2,"nextCursor":""}""")
                assertTrue(cache.refresh("owner"))
                assertEquals(listOf("new.jpg", "second.jpg"), cache.load("owner").map { it.name })
                assertEquals(listOf("old.jpg"), cache.load("other").map { it.name })
                val requests = List(server.requestCount) { server.takeRequest() }
                assertTrue(requests.any { it.requestUrl?.queryParameter("after") == "page/+=" })
                assertTrue(requests.filter { it.requestUrl?.encodedPath == "/api/photos" }
                    .all { it.requestUrl?.queryParameter("snapshot") == "next" })
                reply("""{"token":"next","paged":true}""")
                assertFalse(cache.refresh("owner"))
            }
        }
    }
}
