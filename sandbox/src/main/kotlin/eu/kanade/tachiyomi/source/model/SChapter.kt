// Adapted from Mihon/Tachiyomi (https://github.com/mihonapp/mihon), licensed under Apache License 2.0.
// See /NOTICE.md and /THIRD_PARTY_LICENSES/Apache-2.0.txt for full attribution.

package eu.kanade.tachiyomi.source.model

@Suppress("unused")
interface SChapter {

    var url: String

    var name: String

    var date_upload: Long

    var chapter_number: Float

    var scanlator: String?

    var memo: kotlinx.serialization.json.JsonObject?

    companion object {
        fun create(): SChapter {
            return SChapterImpl()
        }
    }
}
