package eu.kanade.tachiyomi.network

import okhttp3.Cookie
import okhttp3.HttpUrl.Companion.toHttpUrl
import kotlin.test.*
import java.nio.file.Files

class PersistentCookieStoreTest {
    @Test fun honorsDomainPathExpiryAndSameNameCookies() {
        val store=PersistentCookieStore()
        val url="https://source.example/reader/1".toHttpUrl()
        store.addAll(url,listOf(
            Cookie.Builder().name("session").value("root").hostOnlyDomain("source.example").path("/").secure().build(),
            Cookie.Builder().name("session").value("reader").hostOnlyDomain("source.example").path("/reader").build(),
            Cookie.Builder().name("expired").value("gone").hostOnlyDomain("source.example").expiresAt(1).build()
        ))
        assertEquals(2,store.get(url).size)
        assertEquals(1,store.get("https://source.example/other".toHttpUrl()).size)
        assertTrue(store.get("https://evilsource.example/reader/1".toHttpUrl()).isEmpty())
        assertTrue(store.get("https://sub.source.example/reader/1".toHttpUrl()).isEmpty())
        assertEquals(1,store.get("http://source.example/reader/1".toHttpUrl()).size)
    }
    @Test fun persistsOnlyPersistentCookiesAndUserAgent() {
        val directory=Files.createTempDirectory("cookies-test").toFile()
        System.setProperty("tsunagu.cookies.file",directory.resolve("cookies.json").path)
        try {
            val url="https://source.example/".toHttpUrl()
            val store=PersistentCookieStore()
            store.addAll(url,listOf(Cookie.Builder().name("clearance").value("ok").hostOnlyDomain(url.host).expiresAt(System.currentTimeMillis()+60000).build()))
            store.setUserAgent(url,"Browser Agent")
            val reloaded=PersistentCookieStore()
            assertEquals("ok",reloaded.get(url).single().value)
            assertEquals("Browser Agent",reloaded.userAgent(url))
            assertTrue(reloaded.cookies.single().maxAge>0)
        } finally {System.clearProperty("tsunagu.cookies.file");directory.deleteRecursively()}
    }
}
