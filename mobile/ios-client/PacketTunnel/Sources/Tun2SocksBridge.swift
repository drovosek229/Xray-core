import Foundation
import Tun2SocksKit

struct Tun2SocksConfiguration: Sendable {
    var socksAddress: String
    var socksPort: Int
    var mtu: Int
}

final class Tun2SocksBridge {
    private let stateQueue = DispatchQueue(label: "internet.tun2socks.state")
    private var isRunning = false

    func start(
        configuration: Tun2SocksConfiguration,
        onExit: @escaping @Sendable (Int32) -> Void
    ) {
        let shouldStart = stateQueue.sync { () -> Bool in
            guard !isRunning else {
                return false
            }
            isRunning = true
            return true
        }
        guard shouldStart else {
            return
        }

        let content = """
        tunnel:
          mtu: \(configuration.mtu)
        socks5:
          port: \(configuration.socksPort)
          address: \(configuration.socksAddress)
          udp: 'udp'
        misc:
          task-stack-size: 24576
          tcp-buffer-size: 4096
          max-session-count: 768
          connect-timeout: 5000
          read-write-timeout: 60000
          log-file: stderr
          log-level: error
          limit-nofile: 65535
        """

        Socks5Tunnel.run(withConfig: .string(content: content)) { [weak self] code in
            self?.stateQueue.sync {
                self?.isRunning = false
            }
            onExit(code)
        }
    }

    func stop() {
        let shouldStop = stateQueue.sync { () -> Bool in
            guard isRunning else {
                return false
            }
            isRunning = false
            return true
        }
        guard shouldStop else {
            return
        }
        Socks5Tunnel.quit()
    }
}
