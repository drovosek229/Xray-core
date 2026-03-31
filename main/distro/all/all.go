package all

import (
	// The following are necessary as they register handlers in their init functions.

	// Mandatory features. Can't remove unless there are replacements.
	_ "github.com/drovosek229/Xray-core/app/dispatcher"
	_ "github.com/drovosek229/Xray-core/app/proxyman/inbound"
	_ "github.com/drovosek229/Xray-core/app/proxyman/outbound"

	// Default commander and all its services. This is an optional feature.
	_ "github.com/drovosek229/Xray-core/app/commander"
	_ "github.com/drovosek229/Xray-core/app/log/command"
	_ "github.com/drovosek229/Xray-core/app/proxyman/command"
	_ "github.com/drovosek229/Xray-core/app/stats/command"

	// Developer preview services
	_ "github.com/drovosek229/Xray-core/app/observatory/command"

	// Other optional features.
	_ "github.com/drovosek229/Xray-core/app/dns"
	_ "github.com/drovosek229/Xray-core/app/dns/fakedns"
	_ "github.com/drovosek229/Xray-core/app/log"
	_ "github.com/drovosek229/Xray-core/app/metrics"
	_ "github.com/drovosek229/Xray-core/app/policy"
	_ "github.com/drovosek229/Xray-core/app/reverse"
	_ "github.com/drovosek229/Xray-core/app/router"
	_ "github.com/drovosek229/Xray-core/app/stats"

	// Fix dependency cycle caused by core import in internet package
	_ "github.com/drovosek229/Xray-core/transport/internet/tagged/taggedimpl"

	// Developer preview features
	_ "github.com/drovosek229/Xray-core/app/observatory"
	_ "github.com/drovosek229/Xray-core/app/observatory/burst"

	// Inbound and outbound proxies.
	_ "github.com/drovosek229/Xray-core/proxy/blackhole"
	_ "github.com/drovosek229/Xray-core/proxy/dns"
	_ "github.com/drovosek229/Xray-core/proxy/dokodemo"
	_ "github.com/drovosek229/Xray-core/proxy/freedom"
	_ "github.com/drovosek229/Xray-core/proxy/http"
	_ "github.com/drovosek229/Xray-core/proxy/loopback"
	_ "github.com/drovosek229/Xray-core/proxy/shadowsocks"
	_ "github.com/drovosek229/Xray-core/proxy/socks"
	_ "github.com/drovosek229/Xray-core/proxy/trojan"
	_ "github.com/drovosek229/Xray-core/proxy/vless/inbound"
	_ "github.com/drovosek229/Xray-core/proxy/vless/outbound"
	_ "github.com/drovosek229/Xray-core/proxy/vmess/inbound"
	_ "github.com/drovosek229/Xray-core/proxy/vmess/outbound"
	_ "github.com/drovosek229/Xray-core/proxy/wireguard"

	// Transports
	_ "github.com/drovosek229/Xray-core/transport/internet/grpc"
	_ "github.com/drovosek229/Xray-core/transport/internet/httpupgrade"
	_ "github.com/drovosek229/Xray-core/transport/internet/kcp"
	_ "github.com/drovosek229/Xray-core/transport/internet/reality"
	_ "github.com/drovosek229/Xray-core/transport/internet/splithttp"
	_ "github.com/drovosek229/Xray-core/transport/internet/tcp"
	_ "github.com/drovosek229/Xray-core/transport/internet/tls"
	_ "github.com/drovosek229/Xray-core/transport/internet/udp"
	_ "github.com/drovosek229/Xray-core/transport/internet/websocket"

	// Transport headers
	_ "github.com/drovosek229/Xray-core/transport/internet/headers/http"
	_ "github.com/drovosek229/Xray-core/transport/internet/headers/noop"

	// JSON & TOML & YAML
	_ "github.com/drovosek229/Xray-core/main/json"
	_ "github.com/drovosek229/Xray-core/main/toml"
	_ "github.com/drovosek229/Xray-core/main/yaml"

	// Load config from file or http(s)
	_ "github.com/drovosek229/Xray-core/main/confloader/external"

	// Commands
	_ "github.com/drovosek229/Xray-core/main/commands/all"
)
