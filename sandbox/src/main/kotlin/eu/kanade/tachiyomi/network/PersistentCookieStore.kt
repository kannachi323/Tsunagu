// Adapted from Mihon/Tachiyomi (https://github.com/mihonapp/mihon), licensed under Apache License 2.0.
// See /NOTICE.md and /THIRD_PARTY_LICENSES/Apache-2.0.txt for full attribution.

package eu.kanade.tachiyomi.network

import okhttp3.Cookie
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import java.net.CookieStore
import java.net.HttpCookie
import java.net.URI
import java.io.File
import kotlinx.serialization.Serializable
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json

@Serializable
data class BrowserCookie(val name: String, val value: String, val domain: String, val path: String = "/", val expiresAt: Long = Long.MAX_VALUE, val secure: Boolean = false, val httpOnly: Boolean = false, val hostOnly: Boolean = true, val persistent: Boolean = true) {
    fun cookie(): Cookie = Cookie.Builder().name(name).value(value).path(path)
        .apply { if (hostOnly) hostOnlyDomain(domain) else domain(domain); if (persistent) expiresAt(expiresAt); if (secure) secure(); if (httpOnly) httpOnly() }.build()
    companion object { fun from(c: Cookie) = BrowserCookie(c.name,c.value,c.domain,c.path,c.expiresAt,c.secure,c.httpOnly,c.hostOnly,c.persistent) }
}
@Serializable
data class BrowserState(val cookies: List<BrowserCookie> = emptyList(), val userAgent: String = "")
@Serializable
private data class StoredCookies(val cookies: List<BrowserCookie> = emptyList(), val agents: Map<String,String> = emptyMap())

class PersistentCookieStore : CookieStore {
    private val cookies = mutableListOf<Cookie>()
    private val agents = mutableMapOf<String,String>()
    private val file = System.getProperty("tsunagu.cookies.file")?.let(::File)
    init {
        file?.takeIf { it.exists() }?.let { f -> runCatching {
            val saved = Json.decodeFromString<StoredCookies>(f.readText())
            cookies.addAll(saved.cookies.map { it.cookie() }.filter { it.expiresAt > System.currentTimeMillis() })
            agents.putAll(saved.agents)
        } }
    }
    private fun persist() {
        file?.let { target ->
            target.parentFile.mkdirs()
            val temp = File(target.path + ".tmp")
            temp.writeText(Json.encodeToString(StoredCookies(cookies.filter { it.persistent }.map(BrowserCookie::from),agents.toMap())))
            check(temp.renameTo(target)) { "Could not persist source cookies" }
        }
    }
    @Synchronized fun addAll(url: HttpUrl, values: List<Cookie>) {
        values.forEach { c ->
            // Cookie.matches also checks path; response cookies may legitimately
            // target a different path, so validate the host independently here.
            require(url.host == c.domain || (!c.hostOnly && url.host.endsWith("." + c.domain))) { "Cookie domain does not match source" }
            cookies.removeAll { it.name == c.name && it.domain == c.domain && it.path == c.path }
            if (c.expiresAt > System.currentTimeMillis()) cookies.add(c)
        }
        persist()
    }
    @Synchronized fun get(url: HttpUrl): List<Cookie> {
        cookies.removeAll { it.expiresAt <= System.currentTimeMillis() }
        return cookies.filter { it.matches(url) }
    }
    @Synchronized fun setUserAgent(url: HttpUrl, value: String) {
        require(value.length <= 2048 && !value.contains('\r') && !value.contains('\n'))
        agents[url.host] = value; persist()
    }
    @Synchronized fun userAgent(url: HttpUrl): String? = agents[url.host]
    @Synchronized override fun removeAll(): Boolean { val had = cookies.isNotEmpty(); cookies.clear(); agents.clear(); persist(); return had }
    @Synchronized fun removeSessionCookies() { cookies.removeAll { !it.persistent }; persist() }
    @Synchronized fun remove(uri: URI) { cookies.removeAll { it.domain == uri.host }; agents.remove(uri.host); persist() }
    @Synchronized fun getStoredCookies(): List<Cookie> = cookies.toList()
    override fun get(uri: URI): List<HttpCookie> = get(uri.toString().toHttpUrlOrNull() ?: return emptyList()).map(::toHttpCookie)
    override fun getCookies(): List<HttpCookie> = getStoredCookies().map(::toHttpCookie)
    override fun getURIs(): List<URI> = getStoredCookies().map { URI("https://${it.domain}") }.distinct()
    override fun add(uri: URI?, cookie: HttpCookie) {
        val url = uri?.toString()?.toHttpUrlOrNull() ?: return
        val builder = Cookie.Builder().name(cookie.name).value(cookie.value).path(cookie.path ?: "/")
        if (cookie.domain == null) builder.hostOnlyDomain(url.host) else builder.domain(cookie.domain.removePrefix("."))
        if (cookie.maxAge >= 0) builder.expiresAt(System.currentTimeMillis() + cookie.maxAge.coerceAtMost(315360000) * 1000)
        if (cookie.secure) builder.secure()
        if (cookie.isHttpOnly) builder.httpOnly()
        addAll(url,listOf(builder.build()))
    }
    @Synchronized override fun remove(uri: URI?, cookie: HttpCookie): Boolean {
        val removed = cookies.removeAll { it.name == cookie.name && it.domain == (cookie.domain?.removePrefix(".") ?: uri?.host) && it.path == (cookie.path ?: "/") }
        if (removed) persist(); return removed
    }
    private fun toHttpCookie(c: Cookie) = HttpCookie(c.name,c.value).apply {
        domain = if(c.hostOnly) c.domain else ".${c.domain}"; path=c.path; secure=c.secure; isHttpOnly=c.httpOnly
        maxAge=if(c.persistent) ((c.expiresAt-System.currentTimeMillis())/1000).coerceAtLeast(0) else -1
    }
}
