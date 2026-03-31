package conf

import (
	"strings"

	"github.com/drovosek229/Xray-core/app/commander"
	loggerservice "github.com/drovosek229/Xray-core/app/log/command"
	observatoryservice "github.com/drovosek229/Xray-core/app/observatory/command"
	handlerservice "github.com/drovosek229/Xray-core/app/proxyman/command"
	routerservice "github.com/drovosek229/Xray-core/app/router/command"
	statsservice "github.com/drovosek229/Xray-core/app/stats/command"
	"github.com/drovosek229/Xray-core/common/errors"
	"github.com/drovosek229/Xray-core/common/serial"
)

type APIConfig struct {
	Tag      string   `json:"tag"`
	Listen   string   `json:"listen"`
	Services []string `json:"services"`
}

func (c *APIConfig) Build() (*commander.Config, error) {
	if c.Tag == "" {
		return nil, errors.New("API tag can't be empty.")
	}

	services := make([]*serial.TypedMessage, 0, 16)
	for _, s := range c.Services {
		switch strings.ToLower(s) {
		case "reflectionservice":
			services = append(services, serial.ToTypedMessage(&commander.ReflectionConfig{}))
		case "handlerservice":
			services = append(services, serial.ToTypedMessage(&handlerservice.Config{}))
		case "loggerservice":
			services = append(services, serial.ToTypedMessage(&loggerservice.Config{}))
		case "statsservice":
			services = append(services, serial.ToTypedMessage(&statsservice.Config{}))
		case "observatoryservice":
			services = append(services, serial.ToTypedMessage(&observatoryservice.Config{}))
		case "routingservice":
			services = append(services, serial.ToTypedMessage(&routerservice.Config{}))
		}
	}

	return &commander.Config{
		Tag:     c.Tag,
		Listen:  c.Listen,
		Service: services,
	}, nil
}
