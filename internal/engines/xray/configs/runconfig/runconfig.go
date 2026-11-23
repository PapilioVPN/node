// Package runconfig prepares the same effective Xray configuration for startup and validation.
package runconfig

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type Run struct {
	Args    []string
	Overlay []byte
	Socket  string
	dir     string
}

func Prepare(configFile, runtimeDirectory string) (Run, error) {
	dir, err := os.MkdirTemp(runtimeDirectory, "node-xray-")
	if err != nil {
		return Run{}, err
	}
	socket := filepath.Join(dir, "api.sock")
	// Linux sockaddr_un has 108 bytes including the terminating NUL.
	if len(socket) >= 108 {
		return Run{}, errors.Join(errors.New("xray readiness socket path is too long"), os.RemoveAll(dir))
	}

	// Xray applies the last API block when merging configs.
	var overlay struct {
		API struct {
			Tag      string   `json:"tag"`
			Listen   string   `json:"listen"`
			Services []string `json:"services"`
		} `json:"api"`
	}
	overlay.API.Tag = "papiliovpn-node-api"
	overlay.API.Listen = socket
	overlay.API.Services = []string{"StatsService", "RoutingService"}
	data, err := json.Marshal(overlay)
	if err != nil {
		return Run{}, errors.Join(err, os.RemoveAll(dir))
	}
	return Run{
		Args:    []string{"run", "-format", "json", "-c", configFile, "-c", "stdin:"},
		Overlay: data,
		Socket:  socket,
		dir:     dir,
	}, nil
}

func (run Run) Cleanup() error { return os.RemoveAll(run.dir) }
