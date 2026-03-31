package conf

import (
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/xtls/xray-core/app/observatory"
	"github.com/xtls/xray-core/app/observatory/burst"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/infra/conf/cfgcommon/duration"
)

type ObservatoryConfig struct {
	SubjectSelector   []string          `json:"subjectSelector"`
	ProbeURL          string            `json:"probeURL"`
	ProbeInterval     duration.Duration `json:"probeInterval"`
	EnableConcurrency bool              `json:"enableConcurrency"`
}

func (o *ObservatoryConfig) Build() (proto.Message, error) {
	return &observatory.Config{SubjectSelector: o.SubjectSelector, ProbeUrl: o.ProbeURL, ProbeInterval: int64(o.ProbeInterval), EnableConcurrency: o.EnableConcurrency}, nil
}

type BurstObservatoryConfig struct {
	SubjectSelector []string `json:"subjectSelector"`
	// health check settings
	HealthCheck    *healthCheckSettings    `json:"pingConfig,omitempty"`
	RuntimeFailure *runtimeFailureSettings `json:"runtimeFailure,omitempty"`
}

type runtimeFailureSettings struct {
	BaseBackoff duration.Duration `json:"baseBackoff,omitempty"`
	MaxBackoff  duration.Duration `json:"maxBackoff,omitempty"`
}

func (r *runtimeFailureSettings) Build() *burst.RuntimeFailureConfig {
	if r == nil || r.BaseBackoff <= 0 {
		return nil
	}

	config := &burst.RuntimeFailureConfig{
		BaseBackoff: int64(r.BaseBackoff),
		MaxBackoff:  int64(r.MaxBackoff),
	}
	if config.MaxBackoff <= 0 {
		config.MaxBackoff = int64(time.Duration(config.BaseBackoff) * 8)
	}
	if config.MaxBackoff < config.BaseBackoff {
		config.MaxBackoff = config.BaseBackoff
	}
	return config
}

func (b BurstObservatoryConfig) Build() (proto.Message, error) {
	if b.HealthCheck == nil {
		return nil, errors.New("BurstObservatory requires a valid pingConfig")
	}
	if result, err := b.HealthCheck.Build(); err == nil {
		return &burst.Config{
			SubjectSelector: b.SubjectSelector,
			PingConfig:      result.(*burst.HealthPingConfig),
			RuntimeFailure:  b.RuntimeFailure.Build(),
		}, nil
	} else {
		return nil, err
	}
}
