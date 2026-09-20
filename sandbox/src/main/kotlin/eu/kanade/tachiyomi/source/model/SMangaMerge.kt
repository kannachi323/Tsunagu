// Adapted from Mihon/Tachiyomi (https://github.com/mihonapp/mihon), licensed under Apache License 2.0.
// See /NOTICE.md and /THIRD_PARTY_LICENSES/Apache-2.0.txt for full attribution.

package eu.kanade.tachiyomi.source.model

fun SManga.copyFrom(other: SManga) {
    if (other.title.isNotBlank()) title = other.title
    other.author?.let { if (it.isNotBlank()) author = it }
    other.artist?.let { if (it.isNotBlank()) artist = it }
    other.description?.let { if (it.isNotBlank()) description = it }
    other.genre?.let { if (it.isNotBlank()) genre = it }
    other.thumbnail_url?.let { if (it.isNotBlank()) thumbnail_url = it }
    if (other.status != SManga.UNKNOWN) status = other.status
    update_strategy = other.update_strategy
    other.memo?.let { if (it.isNotEmpty()) memo = it }
    initialized = other.initialized || initialized
}
