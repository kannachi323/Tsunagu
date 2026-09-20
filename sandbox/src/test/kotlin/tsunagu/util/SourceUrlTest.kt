package tsunagu.util

import eu.kanade.tachiyomi.animesource.online.AnimeHttpSource
import eu.kanade.tachiyomi.animesource.model.SEpisode
import eu.kanade.tachiyomi.animesource.model.SAnime
import eu.kanade.tachiyomi.source.online.HttpSource
import eu.kanade.tachiyomi.source.model.SChapter
import kotlin.test.Test
import kotlin.test.assertEquals

class SourceUrlTest {
    private val anime = object : AnimeHttpSource() {
        override val name = "Test"
        override val lang = "en"
        override val supportsLatest = false
        override val baseUrl = "https://source.example"
    }
    private val manga = object : HttpSource() {
        override val name = "Test"
        override val lang = "en"
        override val supportsLatest = false
        override val baseUrl = "https://source.example"
    }

    @Test fun preservesSourceOwnedRelativeIdentifiers() {
        val raw = "kanojo-okarishimasu-5th-season/1"
        with(anime) {
            assertEquals(raw, SEpisode.create().apply { setUrlWithoutDomain(raw) }.url)
            assertEquals(raw, SAnime.create().apply { setUrlWithoutDomain(raw) }.url)
        }
        with(manga) {
            assertEquals(raw, SChapter.create().apply { setUrlWithoutDomain(raw) }.url)
        }
        for (value in listOf("/1", "series/1?lang=en#part", "?id=1", "opaque:id/1")) {
            assertEquals(value, sourceUrlWithoutDomain(value))
        }
    }

    @Test fun removesOnlyTheOriginAndPreservesEscaping() {
        for (origin in listOf("https://source.example", "http://source.example:8080", "//source.example")) {
            assertEquals("/series%20name/1?a=%2F#part", sourceUrlWithoutDomain("$origin/series%20name/1?a=%2F#part"))
        }
        assertEquals("not a URL/1", sourceUrlWithoutDomain("not a URL/1"))
    }
}
