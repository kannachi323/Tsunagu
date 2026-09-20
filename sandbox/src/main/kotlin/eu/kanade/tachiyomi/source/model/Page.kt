// Adapted from Mihon/Tachiyomi (https://github.com/mihonapp/mihon), licensed under Apache License 2.0.
// See /NOTICE.md and /THIRD_PARTY_LICENSES/Apache-2.0.txt for full attribution.

package eu.kanade.tachiyomi.source.model

import android.net.Uri

@Suppress("unused")
class Page(
        val index: Int,
        val url: String = "",
        var imageUrl: String? = null,
        var uri: Uri? = null
)
