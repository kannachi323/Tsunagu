package tsunagu.grpc

import eu.kanade.tachiyomi.source.model.Page
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.async
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.supervisorScope
import kotlinx.coroutines.withTimeout

class PageResolutionTest {
    @Test
    fun resolvesInParallelWithBoundedRequestsAndPreservesOrder() = runBlocking {
        val active = AtomicInteger()
        val maximum = AtomicInteger()
        val calls = AtomicInteger()
        val threeStarted = CompletableDeferred<Unit>()
        val release = CompletableDeferred<Unit>()
        val pages = (0..7).map { Page(it) } + Page(8, imageUrl = "ready")
        val result = async {
            resolvePageURLs(pages) { page ->
                calls.incrementAndGet()
                val count = active.incrementAndGet()
                maximum.updateAndGet { maxOf(it, count) }
                if (count == 3) threeStarted.complete(Unit)
                try { release.await(); "image-${page.index}" }
                finally { active.decrementAndGet() }
            }
        }
        try {
            withTimeout(3000) { threeStarted.await() }
            assertEquals(3, active.get())
        } finally { release.complete(Unit) }
        assertEquals((0..7).map { "image-$it" } + "ready", result.await())
        assertEquals(8, calls.get())
        assertEquals(3, maximum.get())
    }

    @Test
    fun failureCancelsOtherPageRequests() = runBlocking {
        supervisorScope {
            val started = CompletableDeferred<Unit>()
            val cancelled = CompletableDeferred<Unit>()
            val result = async {
                resolvePageURLs(listOf(Page(0), Page(1))) { page ->
                    if (page.index == 1) {
                        started.await()
                        error("source failed")
                    }
                    try {
                        started.complete(Unit)
                        CompletableDeferred<String>().await()
                    } finally { cancelled.complete(Unit) }
                }
            }
            assertFailsWith<IllegalStateException> { result.await() }
            withTimeout(3000) { cancelled.await() }
        }
    }
}
