package iosbridge

import "testing"

func TestVersionAndRunningState(t *testing.T) {
	engine := NewXrayEngine()

	if engine.Version() == "" {
		t.Fatal("expected non-empty version")
	}
	if engine.IsRunning() {
		t.Fatal("expected engine to start stopped")
	}
}

func TestValidateRejectsMalformedJSON(t *testing.T) {
	engine := NewXrayEngine()

	if err := engine.Validate("{"); err == nil {
		t.Fatal("expected malformed config to fail validation")
	}
}

func TestStartStopIsIdempotent(t *testing.T) {
	engine := NewXrayEngine()

	configJSON := `{
	  "outbounds": [
	    {
	      "protocol": "freedom",
	      "tag": "direct"
	    }
	  ]
	}`

	if err := engine.Start(configJSON, -1, ""); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if !engine.IsRunning() {
		t.Fatal("expected running engine after start")
	}
	if err := engine.Start(configJSON, -1, ""); err != nil {
		t.Fatalf("second start failed: %v", err)
	}
	if err := engine.Stop(); err != nil {
		t.Fatalf("stop failed: %v", err)
	}
	if engine.IsRunning() {
		t.Fatal("expected stopped engine after stop")
	}
	if err := engine.Stop(); err != nil {
		t.Fatalf("second stop failed: %v", err)
	}
}
