// Adapted from Aniyomi (https://github.com/aniyomiorg/aniyomi), licensed under Apache License 2.0.
// See /NOTICE.md and /THIRD_PARTY_LICENSES/Apache-2.0.txt for full attribution.

package eu.kanade.tachiyomi.animesource

import androidx.preference.PreferenceScreen

@Suppress("unused")
interface ConfigurableAnimeSource {

    fun setupPreferenceScreen(screen: PreferenceScreen)

}
