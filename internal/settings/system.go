package settings

import (
	"errors"
	"time"
)

type System struct {
	Host Host `yaml:"host"`
}

type Host struct {
	Identity *ReadAccess `yaml:"identity"`
}

// ReadAccess grants only the observation owning this configuration entry.
// A missing entry leaves that capability uninstalled.
type ReadAccess struct {
	AllowRead bool `yaml:"allow_read"`
}

func (config Config) validateSystem(remaining time.Duration) error {
	requests := config.Communications.Requests
	if config.System.Host.Identity == nil && requests.Timeout == 0 && requests.NATS == nil {
		return nil
	}
	if err := requests.validate(config.Communications); err != nil {
		return err
	}
	if config.System.Host.Identity != nil && requests.NATS != nil && requests.NATS.ShutdownTimeout >= remaining {
		return errors.New("request subscription and transport drain budgets must fit inside shutdown_timeout")
	}
	return nil
}
