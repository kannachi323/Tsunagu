package tsunagu

import io.grpc.Attributes
import io.grpc.ServerTransportFilter
import io.grpc.health.v1.HealthCheckRequest
import io.grpc.health.v1.HealthGrpc
import io.grpc.netty.shaded.io.grpc.netty.NettyChannelBuilder
import io.grpc.protobuf.services.HealthStatusManager
import java.net.InetSocketAddress
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.Test
import kotlin.test.assertEquals

class EmbeddedKeepaliveTest {
    @Test
    fun idleClientPingsDoNotForceReconnect() {
        val connections = AtomicInteger()
        val disconnects = AtomicInteger()
        val server = Embedded.serverBuilder(InetSocketAddress("127.0.0.1", 0))
            .addService(HealthStatusManager().healthService)
            .addTransportFilter(object : ServerTransportFilter() {
                override fun transportReady(attributes: Attributes): Attributes {
                    connections.incrementAndGet()
                    return attributes
                }
                override fun transportTerminated(attributes: Attributes) {
                    disconnects.incrementAndGet()
                }
            }).build().start()
        val channel = NettyChannelBuilder.forAddress("127.0.0.1", server.port)
            .usePlaintext()
            .keepAliveTime(20, TimeUnit.SECONDS)
            .keepAliveTimeout(5, TimeUnit.SECONDS)
            .keepAliveWithoutCalls(true)
            .build()
        try {
            HealthGrpc.newBlockingStub(channel).withDeadlineAfter(5, TimeUnit.SECONDS)
                .check(HealthCheckRequest.getDefaultInstance())
            // Three idle pings exceed the default server strike limit; old config disconnects.
            Thread.sleep(75_000)
            assertEquals(0, disconnects.get(), "Idle keepalive must not receive GOAWAY")
            HealthGrpc.newBlockingStub(channel).withDeadlineAfter(5, TimeUnit.SECONDS)
                .check(HealthCheckRequest.getDefaultInstance())
            assertEquals(1, connections.get(), "The original connection must remain usable")
        } finally {
            channel.shutdownNow().awaitTermination(5, TimeUnit.SECONDS)
            server.shutdownNow().awaitTermination(5, TimeUnit.SECONDS)
        }
    }
}
