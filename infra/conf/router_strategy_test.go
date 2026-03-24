package conf

import (
	"testing"
	"time"

	"github.com/xtls/xray-core/app/router"
	"github.com/xtls/xray-core/infra/conf/cfgcommon/duration"
)

func TestStrategyLeastLoadConfigBuildSetsNewSmoothingFields(t *testing.T) {
	config, err := (&strategyLeastLoadConfig{
		Expected:      2,
		MaxRTT:        duration.Duration(2 * time.Second),
		Tolerance:     1.5,
		MinSamples:    -1,
		SoftFailGrace: 3,
	}).Build()
	if err != nil {
		t.Fatal("expected leastload config to build:", err)
	}

	result := config.(*router.StrategyLeastLoadConfig)
	if result.GetMinSamples() != 0 {
		t.Fatalf("expected negative minSamples to clamp to 0, got %d", result.GetMinSamples())
	}
	if result.GetSoftFailGrace() != 3 {
		t.Fatalf("expected softFailGrace to be preserved, got %d", result.GetSoftFailGrace())
	}
	if result.GetTolerance() != 1 {
		t.Fatalf("expected tolerance to clamp to 1, got %f", result.GetTolerance())
	}
	if result.GetMaxRTT() != int64(2*time.Second) {
		t.Fatalf("expected maxRTT to be preserved, got %d", result.GetMaxRTT())
	}
}

func TestStrategyMostStableConfigBuildAppliesDefaultsAndClamps(t *testing.T) {
	config, err := (&strategyMostStableConfig{
		MaxRTT:               duration.Duration(-1 * time.Second),
		Tolerance:            1.5,
		MinSamples:           0,
		HoldDown:             0,
		RecoveryObservations: 0,
	}).Build()
	if err != nil {
		t.Fatal("expected moststable config to build:", err)
	}

	result := config.(*router.StrategyMostStableConfig)
	if result.GetMaxRTT() != 0 {
		t.Fatalf("expected negative maxRTT to clamp to 0, got %d", result.GetMaxRTT())
	}
	if result.GetTolerance() != 1 {
		t.Fatalf("expected tolerance to clamp to 1, got %f", result.GetTolerance())
	}
	if result.GetMinSamples() != 4 {
		t.Fatalf("expected default minSamples to be 4, got %d", result.GetMinSamples())
	}
	if result.GetHoldDown() != int64(30*time.Second) {
		t.Fatalf("expected default holdDown to be 30s, got %d", result.GetHoldDown())
	}
	if result.GetRecoveryObservations() != 2 {
		t.Fatalf("expected default recoveryObservations to be 2, got %d", result.GetRecoveryObservations())
	}
}
