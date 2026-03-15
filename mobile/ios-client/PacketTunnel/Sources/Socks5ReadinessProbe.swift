import Darwin
import Foundation

enum Socks5ReadinessProbe {
    static func waitUntilReady(
        host: String,
        port: Int,
        timeout: TimeInterval,
        retryInterval: TimeInterval
    ) -> Bool {
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            if probe(host: host, port: port, timeout: retryInterval) {
                return true
            }
            Thread.sleep(forTimeInterval: retryInterval)
        }
        return false
    }

    static func probe(host: String, port: Int, timeout: TimeInterval) -> Bool {
        let socketFD = socket(AF_INET, SOCK_STREAM, 0)
        guard socketFD >= 0 else {
            return false
        }
        defer {
            close(socketFD)
        }

        var timeoutValue = timeval(
            tv_sec: Int(timeout.rounded(.down)),
            tv_usec: Int32((timeout - floor(timeout)) * 1_000_000)
        )
        guard
            setsockopt(
                socketFD,
                SOL_SOCKET,
                SO_RCVTIMEO,
                &timeoutValue,
                socklen_t(MemoryLayout<timeval>.size)
            ) == 0,
            setsockopt(
                socketFD,
                SOL_SOCKET,
                SO_SNDTIMEO,
                &timeoutValue,
                socklen_t(MemoryLayout<timeval>.size)
            ) == 0
        else {
            return false
        }

        var address = sockaddr_in()
        address.sin_len = UInt8(MemoryLayout<sockaddr_in>.stride)
        address.sin_family = sa_family_t(AF_INET)
        address.sin_port = in_port_t(UInt16(port).bigEndian)
        guard inet_pton(AF_INET, host, &address.sin_addr) == 1 else {
            return false
        }

        let didConnect = withUnsafePointer(to: &address) { pointer in
            pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) { socketAddress in
                connect(socketFD, socketAddress, socklen_t(MemoryLayout<sockaddr_in>.stride))
            }
        }
        guard didConnect == 0 else {
            return false
        }

        let greeting: [UInt8] = [0x05, 0x01, 0x00]
        let sent = greeting.withUnsafeBytes { bytes in
            send(socketFD, bytes.baseAddress, bytes.count, 0)
        }
        guard sent == greeting.count else {
            return false
        }

        var response = [UInt8](repeating: 0, count: 2)
        let received = response.withUnsafeMutableBytes { bytes in
            recv(socketFD, bytes.baseAddress, bytes.count, 0)
        }
        guard received == 2 else {
            return false
        }

        return response[0] == 0x05 && response[1] == 0x00
    }
}
