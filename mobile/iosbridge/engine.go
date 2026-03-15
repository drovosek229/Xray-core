package iosbridge

import (
	"bytes"
	"os"
	"strconv"
	"sync"

	"github.com/xtls/xray-core/common/platform"
	"github.com/xtls/xray-core/core"
	_ "github.com/xtls/xray-core/main/distro/all"
)

type XrayEngine struct {
	mu       sync.Mutex
	instance *core.Instance
}

func NewXrayEngine() *XrayEngine {
	return &XrayEngine{}
}

func (e *XrayEngine) Version() string {
	return core.Version()
}

func (e *XrayEngine) IsRunning() bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	return e.instance != nil && e.instance.IsRunning()
}

func (e *XrayEngine) Validate(configJSON string) error {
	_, err := core.LoadConfig("json", bytes.NewReader([]byte(configJSON)))
	return err
}

func (e *XrayEngine) Start(configJSON string, tunFD int, assetDir string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.instance != nil && e.instance.IsRunning() {
		return nil
	}

	if tunFD >= 0 {
		if err := os.Setenv(platform.TunFdKey, strconv.Itoa(tunFD)); err != nil {
			return err
		}
	}

	if assetDir != "" {
		if err := os.Setenv(platform.AssetLocation, assetDir); err != nil {
			return err
		}
	}

	config, err := core.LoadConfig("json", bytes.NewReader([]byte(configJSON)))
	if err != nil {
		return err
	}

	instance, err := core.New(config)
	if err != nil {
		return err
	}
	if err := instance.Start(); err != nil {
		_ = instance.Close()
		return err
	}

	e.instance = instance
	return nil
}

func (e *XrayEngine) Stop() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.instance == nil {
		return nil
	}

	err := e.instance.Close()
	e.instance = nil
	return err
}
