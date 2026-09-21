package tsunagu

import eu.kanade.tachiyomi.network.NetworkHelper
import io.grpc.*
import io.grpc.netty.shaded.io.grpc.netty.NettyServerBuilder
import io.grpc.netty.shaded.io.netty.channel.nio.NioEventLoopGroup
import io.grpc.netty.shaded.io.netty.channel.socket.nio.NioServerSocketChannel
import java.io.File
import java.net.InetSocketAddress
import java.net.URLConnection
import java.security.MessageDigest
import java.util.concurrent.TimeUnit
import kotlinx.serialization.json.Json
import org.koin.core.context.startKoin
import org.koin.dsl.module
import tsunagu.grpc.ExtensionServiceImpl
import tsunagu.novel.PluginStorage
import tsunagu.registry.ExtensionRegistry
import tsunagu.source.GetSource

/** App-process entry point. Does not exit, spawn a process, or load native TLS. */
object Embedded {
    private var server: Server? = null

    // Match Main.kt: the Go client sends idle pings every 20 seconds.
    internal fun serverBuilder(address: InetSocketAddress): NettyServerBuilder =
        NettyServerBuilder.forAddress(address)
            .permitKeepAliveTime(15, TimeUnit.SECONDS)
            .permitKeepAliveWithoutCalls(true)

    @JvmStatic @Synchronized
    fun start(dataDirectory: String, token: String): Int {
        check(server == null) { "Embedded sandbox already started" }
        require(token.length >= 32)
        URLConnection.setDefaultUseCaches("jar", false)
        System.setProperty("org.graalvm.launcher.home", System.getProperty("java.home"))
        System.setProperty("truffle.UseFallbackRuntime", "true")
        System.setProperty("polyglot.engine.WarnInterpreterOnly", "false")
        val storage = File(dataDirectory, "plugin-storage").apply { mkdirs() }
        System.setProperty("java.io.tmpdir", File(dataDirectory, "tmp").apply { mkdirs() }.path)
        System.setProperty("tsunagu.cookies.file", File(storage, "source-cookies.json").path)
        PluginStorage.baseDir = storage
        android.app.Application.prefsRoot = File(storage, "shared-prefs")
        startKoin {
            modules(module {
                single { Json { ignoreUnknownKeys = true } }
                single { NetworkHelper() }
                single { android.app.Application() }
            })
        }
        val registry = ExtensionRegistry(File(dataDirectory, "extensions"), true)
        GetSource.bind(registry)
        registry.loadAll()
        val auth = object : ServerInterceptor {
            override fun <ReqT : Any?, RespT : Any?> interceptCall(
                call: ServerCall<ReqT, RespT>, headers: Metadata,
                next: ServerCallHandler<ReqT, RespT>
            ): ServerCall.Listener<ReqT> {
                val supplied = headers.get(Metadata.Key.of("authorization", Metadata.ASCII_STRING_MARSHALLER)) ?: ""
                if (!MessageDigest.isEqual(supplied.toByteArray(), "Bearer $token".toByteArray())) {
                    call.close(Status.UNAUTHENTICATED, Metadata())
                    return object : ServerCall.Listener<ReqT>() {}
                }
                return next.startCall(call, headers)
            }
        }
        val boss = NioEventLoopGroup(1)
        val worker = NioEventLoopGroup(2)
        try {
            server = serverBuilder(InetSocketAddress("127.0.0.1", 0))
                .channelType(NioServerSocketChannel::class.java)
                .bossEventLoopGroup(boss).workerEventLoopGroup(worker)
                .addService(ServerInterceptors.intercept(ExtensionServiceImpl(registry), auth))
                .build().start()
            return server!!.port
        } catch (failure: Throwable) {
            boss.shutdownGracefully(); worker.shutdownGracefully()
            throw IllegalStateException(failure.stackTraceToString(), failure)
        }
    }
}
