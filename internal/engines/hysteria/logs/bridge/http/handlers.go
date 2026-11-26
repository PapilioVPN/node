// Package http exposes live redacted Hysteria events as NDJSON.
package http

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/PapilioVPN/node/internal/engines/hysteria/internal/httpapi"
	"github.com/PapilioVPN/node/internal/engines/hysteria/logs/live"
	"github.com/go-chi/chi/v5"
)

const writeTimeout = 2 * time.Second

func Register(router chi.Router, s *live.Service) {
	router.Get(httpapi.InstancePath+"/logs", func(w http.ResponseWriter, r *http.Request) {
		sub, err := s.Subscribe(r.Context(), httpapi.ID(r))
		if err != nil {
			httpapi.WriteError(w, err)
			return
		}
		defer sub.Close()
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/x-ndjson")
		controller := http.NewResponseController(w)
		if controller.SetWriteDeadline(time.Now().Add(writeTimeout)) != nil || flush(controller) != nil {
			return
		}
		enc := json.NewEncoder(w)
		for {
			event, err := sub.Next(r.Context())
			if err != nil {
				return
			}
			if controller.SetWriteDeadline(time.Now().Add(writeTimeout)) != nil || enc.Encode(event) != nil || flush(controller) != nil {
				return
			}
		}
	})
}

func flush(c *http.ResponseController) error {
	if err := c.Flush(); err != nil {
		return err
	}
	return c.SetWriteDeadline(time.Time{})
}
