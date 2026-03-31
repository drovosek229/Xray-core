package conf

import (
	"testing"
	"time"

	"github.com/drovosek229/Xray-core/app/observatory/burst"
	"github.com/drovosek229/Xray-core/infra/conf/cfgcommon/duration"
)

func TestBurstObservatoryConfigBuildNormalizesRuntimeFailure(t *testing.T) {
	config, err := (BurstObservatoryConfig{
		SubjectSelector: []string{"proxy-"},
		HealthCheck:     &healthCheckSettings{},
		RuntimeFailure: &runtimeFailureSettings{
			BaseBackoff: duration.Duration(5 * time.Second),
		},
	}).Build()
	if err != nil {
		t.Fatal("expected burst observatory config to build:", err)
	}

	result := config.(*burst.Config)
	if result.GetRuntimeFailure() == nil {
		t.Fatal("expected runtime failure config to be present")
	}
	if result.GetRuntimeFailure().GetBaseBackoff() != int64(5*time.Second) {
		t.Fatalf("expected baseBackoff to be preserved, got %d", result.GetRuntimeFailure().GetBaseBackoff())
	}
	if result.GetRuntimeFailure().GetMaxBackoff() != int64(40*time.Second) {
		t.Fatalf("expected maxBackoff to default to 8x baseBackoff, got %d", result.GetRuntimeFailure().GetMaxBackoff())
	}
}

func TestBurstObservatoryConfigBuildDisablesRuntimeFailureWithoutPositiveBaseBackoff(t *testing.T) {
	config, err := (BurstObservatoryConfig{
		SubjectSelector: []string{"proxy-"},
		HealthCheck:     &healthCheckSettings{},
		RuntimeFailure: &runtimeFailureSettings{
			BaseBackoff: 0,
			MaxBackoff:  duration.Duration(10 * time.Second),
		},
	}).Build()
	if err != nil {
		t.Fatal("expected burst observatory config to build:", err)
	}

	result := config.(*burst.Config)
	if result.GetRuntimeFailure() != nil {
		t.Fatalf("expected runtime failure config to be omitted when baseBackoff <= 0, got %+v", result.GetRuntimeFailure())
	}
}
