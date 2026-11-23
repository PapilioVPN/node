package http

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/PapilioVPN/node/internal/engines/xray/logs/live"
	"github.com/go-chi/chi/v5"
)

const writeTimeout = 2 * time.Second

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
		var line []byte
		for {
			event, err := sub.Next(r.Context())
			if err != nil {
				return
			}
			if err := controller.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
				return
			}
			line = appendRecord(line[:0], event)
			if _, err := w.Write(line); err != nil || flush(controller) != nil {
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

func appendRecord(line []byte, event live.Event) []byte {
	line = append(line, `{"execution":"`...)
	line = strconv.AppendUint(line, event.Execution, 10)
	line = append(line, `","sequence":"`...)
	line = strconv.AppendUint(line, event.Sequence, 10)
	line = append(line, `","source":`...)
	if event.Source == live.Stderr {
		line = append(line, `"stderr"`...)
	} else {
		line = append(line, `"stdout"`...)
	}
	line = append(line, `,"received_unix_nano":"`...)
	line = strconv.AppendInt(line, event.ReceivedUnixNano, 10)
	line = append(line, `","message":"`...)
	line = base64.StdEncoding.AppendEncode(line, []byte(event.Message))
	line = append(line, `","truncated":`...)
	line = strconv.AppendBool(line, event.Truncated)
	return append(line, '}', '\n')
}
