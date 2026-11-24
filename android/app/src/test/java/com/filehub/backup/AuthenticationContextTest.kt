package com.filehub.backup

import okhttp3.Request
import okhttp3.mockwebserver.Dispatcher
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import org.junit.After
import org.junit.Assert.*
import org.junit.Test
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit

class AuthenticationContextTest {
    @After fun clear() { Net.clear() }

    @Test fun cleanupAllowsOnlyPrivateOnLinkIpv4WithoutHttps() {
        val phone = byteArrayOf(192.toByte(), 168.toByte(), 17, 143.toByte())
        assertTrue(Net.isDirectLocalIPv4("192.168.17.49", phone, 24))
        assertFalse(Net.isDirectLocalIPv4("192.168.18.49", phone, 24))
        assertFalse(Net.isDirectLocalIPv4("192.168.17.49.example.org", phone, 24))
        assertFalse(Net.isDirectLocalIPv4("203.0.113.45", byteArrayOf(203.toByte(), 0, 113, 11), 24))
        assertFalse(Net.isDirectLocalIPv4("192.168.18.49", phone, 0))
    }

    @Test fun defaultsToHttpsForNamesOutsideTheLan() {
        assertEquals("https://filehub.example.net:8080/Base", Net.normalizedServer("filehub.example.net:8080/Base"))
        assertEquals("http://192.168.17.49:8080", Net.normalizedServer("192.168.17.49:8080"))
        assertEquals("http://files.local:443", Net.normalizedServer("files.local:443"))
        assertEquals("http://filehub.example.net", Net.normalizedServer("http://filehub.example.net"))
        assertEquals("https://10.example.2.3.4", Net.normalizedServer("10.example.2.3.4"))
    }

    @Test fun backgroundBackupRequiresExplicitHttpsForLanAddresses() {
        assertFalse(Net.supportsBackgroundBackup("192.168.17.49:8080"))
        assertFalse(Net.supportsBackgroundBackup("http://filehub.example.net"))
        assertFalse(Net.supportsBackgroundBackup(""))
        assertTrue(Net.supportsBackgroundBackup("https://filehub.example.net"))
        assertTrue(Net.supportsBackgroundBackup("https://files.local"))
    }

    @Test fun offlineScopesKeepRegularInstallsAndSeparateCaseSensitiveEndpoints() {
        assertEquals("192.168.17.49:8080\u001fowner", Prefs.accountScope("192.168.17.49:8080", "owner"))
        assertEquals("http://files.local:8080\u001fowner", Prefs.accountScope("http://Files.Local:8080/", "owner"))
        val upper = Prefs.accountScope("https://files.example/Family/", "owner")
        val lower = Prefs.accountScope("https://files.example/family/", "owner")
        assertNotEquals(upper, lower)
        assertNotEquals("https://files.example/family\u001fowner", lower)
        assertEquals(upper, Prefs.accountScope("https://FILES.EXAMPLE/Family", "owner"))
    }

    @Test fun overlappingLoginKeepsCredentialsAtTheirOwnRecipient() {
        val a=MockWebServer(); val b=MockWebServer()
        val entered=CountDownLatch(1); val release=CountDownLatch(1)
        a.dispatcher=object: Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                entered.countDown()
                check(release.await(10,TimeUnit.SECONDS))
                return MockResponse().setHeader("Set-Cookie","fh_session=old; Path=/").setBody("ok")
            }
        }
        b.enqueue(MockResponse().setHeader("Set-Cookie","fh_session=new; Path=/").setBody("ok"))
        a.start(); b.start(); val pool=Executors.newSingleThreadExecutor()
        try {
            val older=pool.submit<Boolean> { Api.login(a.url("/").toString(),"old-user","old-secret",replaceAccount=true) }
            assertTrue(entered.await(5,TimeUnit.SECONDS))
            assertTrue(Api.login(b.url("/").toString(),"new-user","new-secret",replaceAccount=true))
            release.countDown(); assertFalse(older.get(5,TimeUnit.SECONDS))
            assertEquals(b.url("/").toString().trimEnd('/'),Net.server)
            assertEquals("new-user",Net.user)
            val bodyA=a.takeRequest(5,TimeUnit.SECONDS)!!.body.readUtf8()
            val bodyB=b.takeRequest(5,TimeUnit.SECONDS)!!.body.readUtf8()
            assertTrue(bodyA.contains("old-secret")); assertFalse(bodyA.contains("new-secret"))
            assertTrue(bodyB.contains("new-secret")); assertFalse(bodyB.contains("old-secret"))
            assertEquals("new",Net.client.cookieJar.loadForRequest(b.url("/")).single().value)
        } finally { release.countDown(); pool.shutdownNow(); a.shutdown(); b.shutdown() }
    }

    @Test fun logoutInvalidatesAnInFlightLogin() {
        val server=MockWebServer(); val entered=CountDownLatch(1); val release=CountDownLatch(1)
        server.dispatcher=object: Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                entered.countDown(); check(release.await(10,TimeUnit.SECONDS))
                return MockResponse().setHeader("Set-Cookie","fh_session=stale; Path=/")
            }
        }
        server.start(); val pool=Executors.newSingleThreadExecutor()
        try {
            val pending=pool.submit<Boolean>{Api.login(server.url("/").toString(),"owner","synthetic-pass",replaceAccount=true)}
            assertTrue(entered.await(5,TimeUnit.SECONDS)); Net.clear(); release.countDown()
            assertFalse(pending.get(5,TimeUnit.SECONDS)); assertFalse(Net.loggedIn()); assertEquals("",Net.user)
            assertFalse(Api.login(server.url("/").toString(),"owner","synthetic-pass"))
        } finally {release.countDown();pool.shutdownNow();server.shutdown()}
    }

    @Test fun passwordPostDoesNotFollowRedirects() {
        val a=MockWebServer();val b=MockWebServer(); a.start();b.start()
        try {
            a.enqueue(MockResponse().setResponseCode(307).setHeader("Location",b.url("/api/login")))
            assertFalse(Api.login(a.url("/").toString(),"owner","synthetic-pass",replaceAccount=true))
            assertEquals(0,b.requestCount); assertFalse(Net.loggedIn())
        } finally {a.shutdown();b.shutdown()}
    }

    @Test fun staleClientCannotReloginUsingTheNewAccount() {
        val a=MockWebServer();val b=MockWebServer(); a.start();b.start()
        try {
            a.enqueue(MockResponse().setHeader("Set-Cookie","fh_session=a; Path=/"))
            assertTrue(Api.login(a.url("/").toString(),"first","first-pass",replaceAccount=true))
            val oldClient=Net.client
            b.enqueue(MockResponse().setHeader("Set-Cookie","fh_session=b; Path=/"))
            assertTrue(Api.login(b.url("/").toString(),"second","second-pass",replaceAccount=true))
            a.enqueue(MockResponse().setResponseCode(401))
            oldClient.newCall(Request.Builder().url(a.url("/private")).build()).execute().use{assertEquals(401,it.code)}
            assertEquals(2,a.requestCount);assertEquals(1,b.requestCount);assertEquals("second",Net.user)
        } finally {a.shutdown();b.shutdown()}
    }
    @Test fun sessionCookieIsNotSentToAnotherPortOnTheSameHost() {
        val a=MockWebServer();val b=MockWebServer();a.start();b.start()
        try {
            val stale=Request.Builder().url(a.url("/private")).build()
            b.enqueue(MockResponse().setHeader("Set-Cookie","fh_session=recipient-b; Path=/"))
            assertTrue(Api.login(b.url("/").toString(),"owner","synthetic-pass",replaceAccount=true))
            a.enqueue(MockResponse().setResponseCode(401))
            Net.client.newCall(stale).execute().close()
            assertNull(a.takeRequest(5,TimeUnit.SECONDS)!!.getHeader("Cookie"))
            assertEquals(1,b.requestCount)
        } finally {a.shutdown();b.shutdown()}
    }

    @Test fun unavailableServerKeepsItsOwnContextForOfflineUse() {
        val server=MockWebServer();server.start()
        try {
            server.enqueue(MockResponse().setResponseCode(503))
            assertFalse(Api.login(server.url("/").toString(),"owner","synthetic-pass",replaceAccount=true))
            assertEquals(server.url("/api/photos").toString(),Net.url("/api/photos"))
            assertFalse(Net.loggedIn())
            // The same selected session can reauthenticate after connectivity returns.
            server.enqueue(MockResponse().setResponseCode(401))
            server.enqueue(MockResponse().setHeader("Set-Cookie","fh_session=reconnected; Path=/"))
            server.enqueue(MockResponse().setBody("restored"))
            Net.client.newCall(Request.Builder().url(Net.url("/api/photos")).build()).execute().use {
                assertEquals("restored",it.body!!.string())
            }
            assertTrue(Net.loggedIn())
        } finally {server.shutdown()}
    }

    @Test fun restoringSavedRecipientDoesNotContactServer() {
        val server=MockWebServer();server.start()
        try {
            server.enqueue(MockResponse().setResponseCode(503))
            assertFalse(Net.login(server.url("/").toString(),"saved","synthetic-password",true))
            assertTrue(Net.restore(server.url("/").toString(),"saved","synthetic-password"))
            assertEquals(1,server.requestCount)
            assertFalse(Net.loggedIn())
            Net.clear()
            assertFalse(Net.restore(server.url("/").toString(),"saved","synthetic-password"))
        }finally{server.shutdown()}
    }
    @Test fun parallelUnauthorizedRequestsShareOneReauthentication() {
        val server=MockWebServer();val firstWave=CountDownLatch(6)
        val logins=java.util.concurrent.atomic.AtomicInteger()
        server.dispatcher=object:Dispatcher() {
            override fun dispatch(request:RecordedRequest):MockResponse {
                if(request.path=="/api/login")return if(logins.incrementAndGet()==1)MockResponse().setResponseCode(503)
                    else MockResponse().setHeader("Set-Cookie","fh_session=renewed; Path=/")
                if(request.getHeader("Cookie")=="fh_session=renewed")return MockResponse().setBody("ready")
                firstWave.countDown();check(firstWave.await(10,TimeUnit.SECONDS))
                return MockResponse().setResponseCode(401)
            }
        }
        server.start();val pool=Executors.newFixedThreadPool(6)
        try {
            assertFalse(Net.login(server.url("/").toString(),"saved","synthetic-password",true))
            val calls=(1..6).map {pool.submit<Boolean> {Net.client.newCall(Request.Builder().url(server.url("/private")).build()).execute().use {it.isSuccessful}}}
            calls.forEach {assertTrue(it.get(15,TimeUnit.SECONDS))}
            assertEquals(2,logins.get()) // Initial unavailable login, then exactly one renewal.
        }finally{pool.shutdownNow();server.shutdown()}
    }
}
