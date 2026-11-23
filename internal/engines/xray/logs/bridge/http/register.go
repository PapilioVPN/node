package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/PapilioVPN/node/internal/engines/xray/logs/live"
	"github.com/go-chi/chi/v5"
)

const writeTimeout = 2 * time.Second

type record struct {
	Execution        uint64      `json:"execution,string"`
	Sequence         uint64      `json:"sequence,string"`
	Source           live.Source `json:"source"`
	ReceivedUnixNano int64       `json:"received_unix_nano,string"`
	Message          []byte      `json:"message"`
	Truncated        bool        `json:"truncated"`
}

func Register(router chi.Router, service *live.Service) {
	router.Get("/v1/xray/instances/{id}/logs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		sub, err := service.Subscribe(chi.URLParam(r, "id"))
		if err != nil {
			code := http.StatusTooManyRequests
			switch {
			case errors.Is(err, live.ErrNotFound):
				code = http.StatusNotFound
			case errors.Is(err, live.ErrInactive):
				code = http.StatusConflict
			}
			http.Error(w, err.Error(), code)
			return
		}
		defer sub.Close()

		controller := http.NewResponseController(w)
		if err := controller.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
			http.Error(w, "xray log streaming is unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		if flush(controller) != nil {
			return
		}
		encoder := json.NewEncoder(w)
		for {
			event, err := sub.Next(r.Context())
			if err != nil {
				return
			}
			if err := controller.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
				return
			}
			if err := encoder.Encode(record{
				Execution: event.Execution, Sequence: event.Sequence, Source: event.Source,
				ReceivedUnixNano: event.ReceivedUnixNano, Message: []byte(event.Message),
				Truncated: event.Truncated,
			}); err != nil || flush(controller) != nil {
				return
			}
		}
	})
}

func flush(controller *http.ResponseController) error {
	if err := controller.Flush(); err != nil {
		return err
	}
	return controller.SetWriteDeadline(time.Time{})
}
