package com.filehub.backup

import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.SocketPolicy
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.concurrent.TimeUnit

class PhotoSearchCancellationTest {
    @After fun clear() { Net.clear() }

    @Test fun cancelledSearchesReleaseConnectionsForTheNextQuery() = runBlocking<Unit> {
        MockWebServer().use { server ->
            server.enqueue(MockResponse().setHeader("Set-Cookie", "fh_session=synthetic; Path=/"))
            assertTrue(Api.login(server.url("/").toString(), "owner", "test-password", replaceAccount = true))
            assertEquals("/api/login", server.takeRequest(3, TimeUnit.SECONDS)?.path)
            repeat(6) { server.enqueue(MockResponse().setSocketPolicy(SocketPolicy.NO_RESPONSE)) }
            val stale = List(6) { i ->
                launch(start = CoroutineStart.UNDISPATCHED) {
                    if (i % 2 == 0) Api.searchPhotosText("old $i") else Api.searchSemantic("old $i")
                }
            }
            try {
                repeat(6) {
                    assertTrue(server.takeRequest(3, TimeUnit.SECONDS)?.requestUrl?.queryParameter("q")?.startsWith("old ") == true)
                }
                withTimeout(3_000) {
                    stale.forEach { it.cancelAndJoin() }
                    server.enqueue(MockResponse().setBody("""{"photos":[{"name":"new.jpg","source":"Disk","path":"new.jpg","modTime":1}],"count":1}"""))
                    val result = Api.searchPhotosText("new")
                    assertTrue(result.available)
                    assertEquals("new.jpg", result.photos.single().path)
                }
            } finally {
                stale.forEach { it.cancel() }
            }
        }
    }
}
