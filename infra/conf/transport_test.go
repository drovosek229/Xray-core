package conf_test

import (
	"encoding/json"
	"testing"

	. "github.com/xtls/xray-core/infra/conf"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/splithttp"
	"google.golang.org/protobuf/proto"
)

func TestSocketConfig(t *testing.T) {
	createParser := func() func(string) (proto.Message, error) {
		return func(s string) (proto.Message, error) {
			config := new(SocketConfig)
			if err := json.Unmarshal([]byte(s), config); err != nil {
				return nil, err
			}
			return config.Build()
		}
	}

	// test "tcpFastOpen": true, queue length 256 is expected. other parameters are tested here too
	expectedOutput := &internet.SocketConfig{
		Mark:           1,
		Tfo:            256,
		DomainStrategy: internet.DomainStrategy_USE_IP,
		DialerProxy:    "tag",
		HappyEyeballs:  &internet.HappyEyeballsConfig{Interleave: 1, TryDelayMs: 0, PrioritizeIpv6: false, MaxConcurrentTry: 4},
	}
	runMultiTestCase(t, []TestCase{
		{
			Input: `{
				"mark": 1,
				"tcpFastOpen": true,
				"domainStrategy": "UseIP",
				"dialerProxy": "tag"
			}`,
			Parser: createParser(),
			Output: expectedOutput,
		},
	})
	if expectedOutput.ParseTFOValue() != 256 {
		t.Fatalf("unexpected parsed TFO value, which should be 256")
	}

	// test "tcpFastOpen": false, disabled TFO is expected
	expectedOutput = &internet.SocketConfig{
		Mark:          0,
		Tfo:           -1,
		HappyEyeballs: &internet.HappyEyeballsConfig{Interleave: 1, TryDelayMs: 0, PrioritizeIpv6: false, MaxConcurrentTry: 4},
	}
	runMultiTestCase(t, []TestCase{
		{
			Input: `{
				"tcpFastOpen": false
			}`,
			Parser: createParser(),
			Output: expectedOutput,
		},
	})
	if expectedOutput.ParseTFOValue() != 0 {
		t.Fatalf("unexpected parsed TFO value, which should be 0")
	}

	// test "tcpFastOpen": 65535, queue length 65535 is expected
	expectedOutput = &internet.SocketConfig{
		Mark:          0,
		Tfo:           65535,
		HappyEyeballs: &internet.HappyEyeballsConfig{Interleave: 1, TryDelayMs: 0, PrioritizeIpv6: false, MaxConcurrentTry: 4},
	}
	runMultiTestCase(t, []TestCase{
		{
			Input: `{
				"tcpFastOpen": 65535
			}`,
			Parser: createParser(),
			Output: expectedOutput,
		},
	})
	if expectedOutput.ParseTFOValue() != 65535 {
		t.Fatalf("unexpected parsed TFO value, which should be 65535")
	}

	// test "tcpFastOpen": -65535, disable TFO is expected
	expectedOutput = &internet.SocketConfig{
		Mark:          0,
		Tfo:           -65535,
		HappyEyeballs: &internet.HappyEyeballsConfig{Interleave: 1, TryDelayMs: 0, PrioritizeIpv6: false, MaxConcurrentTry: 4},
	}
	runMultiTestCase(t, []TestCase{
		{
			Input: `{
				"tcpFastOpen": -65535
			}`,
			Parser: createParser(),
			Output: expectedOutput,
		},
	})
	if expectedOutput.ParseTFOValue() != 0 {
		t.Fatalf("unexpected parsed TFO value, which should be 0")
	}

	// test "tcpFastOpen": 0, no operation is expected
	expectedOutput = &internet.SocketConfig{
		Mark:          0,
		Tfo:           0,
		HappyEyeballs: &internet.HappyEyeballsConfig{Interleave: 1, TryDelayMs: 0, PrioritizeIpv6: false, MaxConcurrentTry: 4},
	}
	runMultiTestCase(t, []TestCase{
		{
			Input: `{
				"tcpFastOpen": 0
			}`,
			Parser: createParser(),
			Output: expectedOutput,
		},
	})
	if expectedOutput.ParseTFOValue() != -1 {
		t.Fatalf("unexpected parsed TFO value, which should be -1")
	}

	// test omit "tcpFastOpen", no operation is expected
	expectedOutput = &internet.SocketConfig{
		Mark:          0,
		Tfo:           0,
		HappyEyeballs: &internet.HappyEyeballsConfig{Interleave: 1, TryDelayMs: 0, PrioritizeIpv6: false, MaxConcurrentTry: 4},
	}
	runMultiTestCase(t, []TestCase{
		{
			Input:  `{}`,
			Parser: createParser(),
			Output: expectedOutput,
		},
	})
	if expectedOutput.ParseTFOValue() != -1 {
		t.Fatalf("unexpected parsed TFO value, which should be -1")
	}

	// test "tcpFastOpen": null, no operation is expected
	expectedOutput = &internet.SocketConfig{
		Mark:          0,
		Tfo:           0,
		HappyEyeballs: &internet.HappyEyeballsConfig{Interleave: 1, TryDelayMs: 0, PrioritizeIpv6: false, MaxConcurrentTry: 4},
	}
	runMultiTestCase(t, []TestCase{
		{
			Input: `{
				"tcpFastOpen": null
			}`,
			Parser: createParser(),
			Output: expectedOutput,
		},
	})
	if expectedOutput.ParseTFOValue() != -1 {
		t.Fatalf("unexpected parsed TFO value, which should be -1")
	}
}

func TestSplitHTTPBehaviorProfileBuild(t *testing.T) {
	legacyConfig := &SplitHTTPConfig{
		Path: "/xhttp",
	}
	legacyMessage, err := legacyConfig.Build()
	if err != nil {
		t.Fatal(err)
	}
	legacyBuilt := legacyMessage.(*splithttp.Config)
	if legacyBuilt.BehaviorProfile != splithttp.BehaviorProfileLegacy {
		t.Fatalf("unexpected legacy behavior profile: %q", legacyBuilt.BehaviorProfile)
	}
	if legacyBuilt.XPaddingMethod != string(splithttp.PaddingMethodRepeatX) {
		t.Fatalf("unexpected legacy xPaddingMethod: %q", legacyBuilt.XPaddingMethod)
	}
	if legacyBuilt.Xmux.GetMaxConcurrency().GetFrom() != 1 {
		t.Fatalf("expected legacy xmux default maxConcurrency=1, got %d", legacyBuilt.Xmux.GetMaxConcurrency().GetFrom())
	}

	balancedConfig := &SplitHTTPConfig{
		Path:            "/xhttp",
		BehaviorProfile: splithttp.BehaviorProfileBalanced,
	}
	balancedMessage, err := balancedConfig.Build()
	if err != nil {
		t.Fatal(err)
	}
	balancedBuilt := balancedMessage.(*splithttp.Config)
	if balancedBuilt.BehaviorProfile != splithttp.BehaviorProfileBalanced {
		t.Fatalf("unexpected balanced behavior profile: %q", balancedBuilt.BehaviorProfile)
	}
	if balancedBuilt.XPaddingMethod != string(splithttp.PaddingMethodTokenish) {
		t.Fatalf("unexpected balanced xPaddingMethod: %q", balancedBuilt.XPaddingMethod)
	}
	if balancedBuilt.Xmux.GetMaxConcurrency().GetFrom() != 0 {
		t.Fatalf("expected balanced xmux defaults to stay unset in config, got %d", balancedBuilt.Xmux.GetMaxConcurrency().GetFrom())
	}
}
