package android.webkit

import eu.kanade.tachiyomi.network.NetworkHelper
import okhttp3.Cookie
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import org.koin.core.context.GlobalContext

class CookieManager private constructor() {
    private var accept = true
    private fun store() = GlobalContext.get().get<NetworkHelper>().cookieStore
    fun setAcceptCookie(value: Boolean) { accept = value }
    fun acceptCookie(): Boolean = accept
    fun setCookie(url: String?, value: String?) {
        if (!accept || url == null || value == null) return
        val target = url.toHttpUrlOrNull() ?: return
        Cookie.parse(target,value)?.let { store().addAll(target,listOf(it)) }
    }
    fun getCookie(url: String?): String? = url?.toHttpUrlOrNull()?.let { target ->
        store().get(target).joinToString("; ") { "${it.name}=${it.value}" }.ifEmpty { null }
    }
    fun removeAllCookie() { store().removeAll() }
    fun removeAllCookies(callback: ValueCallback<Boolean>?) { callback?.onReceiveValue(store().removeAll()) ?: store().removeAll() }
    fun removeSessionCookie() { store().removeSessionCookies() }
    fun hasCookies(): Boolean = store().getStoredCookies().isNotEmpty()
    fun flush() {}
    companion object { private val instance = CookieManager(); @JvmStatic fun getInstance(): CookieManager = instance }
}
