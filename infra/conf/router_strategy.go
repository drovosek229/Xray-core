package conf

import (
	"google.golang.org/protobuf/proto"
	"strings"
	"time"

	"github.com/xtls/xray-core/app/observatory/burst"
	"github.com/xtls/xray-core/app/router"
	"github.com/xtls/xray-core/infra/conf/cfgcommon/duration"
)

const (
	strategyRandom     string = "random"
	strategyLeastPing  string = "leastping"
	strategyRoundRobin string = "roundrobin"
	strategyLeastLoad  string = "leastload"
	strategyMostStable string = "moststable"
)

var (
	strategyConfigLoader = NewJSONConfigLoader(ConfigCreatorCache{
		strategyRandom:     func() interface{} { return new(strategyEmptyConfig) },
		strategyLeastPing:  func() interface{} { return new(strategyEmptyConfig) },
		strategyRoundRobin: func() interface{} { return new(strategyEmptyConfig) },
		strategyLeastLoad:  func() interface{} { return new(strategyLeastLoadConfig) },
		strategyMostStable: func() interface{} { return new(strategyMostStableConfig) },
	}, "type", "settings")
)

type strategyEmptyConfig struct {
}

func (v *strategyEmptyConfig) Build() (proto.Message, error) {
	return nil, nil
}

type strategyLeastLoadConfig struct {
	// weight settings
	Costs []*router.StrategyWeight `json:"costs,omitempty"`
	// ping rtt baselines
	Baselines []duration.Duration `json:"baselines,omitempty"`
	// expected nodes count to select
	Expected int32 `json:"expected,omitempty"`
	// max acceptable rtt, filter away high delay nodes. default 0
	MaxRTT duration.Duration `json:"maxRTT,omitempty"`
	// acceptable failure rate
	Tolerance float64 `json:"tolerance,omitempty"`
	// minimum burst samples before a candidate can replace the current healthy winner
	MinSamples int32 `json:"minSamples,omitempty"`
	// consecutive soft-fail cycles allowed for the current winner
	SoftFailGrace int32 `json:"softFailGrace,omitempty"`
}

type strategyMostStableConfig struct {
	// weight settings
	Costs []*router.StrategyWeight `json:"costs,omitempty"`
	// max acceptable rtt, filter away high delay nodes. default 0
	MaxRTT duration.Duration `json:"maxRTT,omitempty"`
	// acceptable failure rate. default 0.2
	Tolerance float64 `json:"tolerance,omitempty"`
	// minimum burst samples before a challenger can replace a healthy winner. default 4
	MinSamples int32 `json:"minSamples,omitempty"`
	// quarantine duration after runtime or observed failures. default 30s
	HoldDown duration.Duration `json:"holdDown,omitempty"`
	// successful observation cycles required after hold-down before re-entry. default 2
	RecoveryObservations int32 `json:"recoveryObservations,omitempty"`
}

// healthCheckSettings holds settings for health Checker
type healthCheckSettings struct {
	Destination   string            `json:"destination"`
	Connectivity  string            `json:"connectivity"`
	Interval      duration.Duration `json:"interval"`
	SamplingCount int               `json:"sampling"`
	Timeout       duration.Duration `json:"timeout"`
	HttpMethod    string            `json:"httpMethod"`
}

func (h healthCheckSettings) Build() (proto.Message, error) {
	var httpMethod string
	if h.HttpMethod == "" {
		httpMethod = "HEAD"
	} else {
		httpMethod = strings.TrimSpace(h.HttpMethod)
	}
	return &burst.HealthPingConfig{
		Destination:   h.Destination,
		Connectivity:  h.Connectivity,
		Interval:      int64(h.Interval),
		Timeout:       int64(h.Timeout),
		SamplingCount: int32(h.SamplingCount),
		HttpMethod:    httpMethod,
	}, nil
}

// Build implements Buildable.
func (v *strategyLeastLoadConfig) Build() (proto.Message, error) {
	config := &router.StrategyLeastLoadConfig{}
	config.Costs = v.Costs
	config.Tolerance = float32(v.Tolerance)
	if config.Tolerance < 0 {
		config.Tolerance = 0
	}
	if config.Tolerance > 1 {
		config.Tolerance = 1
	}
	config.Expected = v.Expected
	if config.Expected < 0 {
		config.Expected = 0
	}
	config.MinSamples = v.MinSamples
	if config.MinSamples < 0 {
		config.MinSamples = 0
	}
	config.SoftFailGrace = v.SoftFailGrace
	if config.SoftFailGrace < 0 {
		config.SoftFailGrace = 0
	}
	config.MaxRTT = int64(v.MaxRTT)
	if config.MaxRTT < 0 {
		config.MaxRTT = 0
	}
	config.Baselines = make([]int64, 0)
	for _, b := range v.Baselines {
		if b <= 0 {
			continue
		}
		config.Baselines = append(config.Baselines, int64(b))
	}
	return config, nil
}

// Build implements Buildable.
func (v *strategyMostStableConfig) Build() (proto.Message, error) {
	config := &router.StrategyMostStableConfig{}
	config.Costs = v.Costs
	config.MaxRTT = int64(v.MaxRTT)
	if config.MaxRTT < 0 {
		config.MaxRTT = 0
	}
	config.Tolerance = float32(v.Tolerance)
	if config.Tolerance <= 0 {
		config.Tolerance = 0.2
	}
	if config.Tolerance > 1 {
		config.Tolerance = 1
	}
	config.MinSamples = v.MinSamples
	if config.MinSamples <= 0 {
		config.MinSamples = 4
	}
	config.HoldDown = int64(v.HoldDown)
	if config.HoldDown <= 0 {
		config.HoldDown = int64(30 * time.Second)
	}
	config.RecoveryObservations = v.RecoveryObservations
	if config.RecoveryObservations <= 0 {
		config.RecoveryObservations = 2
	}
	return config, nil
}
