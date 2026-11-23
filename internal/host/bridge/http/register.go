// Package http exposes Host observations over HTTP.
package http

import (
	"github.com/PapilioVPN/node/internal/host/cpu"
	"github.com/go-chi/chi/v5"
)

func Register(router *chi.Mux, sampler *cpu.Sampler, storagePath string) {
	router.Get("/v1/host/uptime", handleUptime)
	router.Get("/v1/host/memory", handleMemory)
	router.Get("/v1/host/cpu", handleCPU(sampler))
	router.Get("/v1/host/storage", handleStorage(storagePath))
	router.Get("/v1/host/stats", handleSystemStats(sampler, storagePath))
}
