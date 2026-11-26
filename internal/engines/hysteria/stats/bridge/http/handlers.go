// Package http exposes Hysteria statistics and disconnect requests.
package http

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"github.com/PapilioVPN/node/internal/engines/hysteria/internal/fault"
	"github.com/PapilioVPN/node/internal/engines/hysteria/internal/httpapi"
	"github.com/PapilioVPN/node/internal/engines/hysteria/stats/observations"
	"github.com/go-chi/chi/v5"
)

type entry struct {
	UserID string `json:"user_id"`
	TX     uint64 `json:"tx_bytes,string"`
	RX     uint64 `json:"rx_bytes,string"`
}
type snapshot struct {
	Entries    []entry `json:"entries"`
	ResetScope string  `json:"reset_scope,omitempty"`
}
type deviceCount struct {
	UserID  string `json:"user_id"`
	Devices uint64 `json:"devices,string"`
}

func Register(router chi.Router, s *observations.Service) {
	base := httpapi.InstancePath + "/stats"
	router.Get(base+"/runtime", func(w http.ResponseWriter, r *http.Request) {
		v, err := s.GetRuntimeStats(r.Context(), httpapi.ID(r))
		if err != nil {
			httpapi.WriteError(w, err)
			return
		}
		httpapi.WriteJSON(w, 200, struct {
			Execution uint64 `json:"execution,string"`
			Since     int64  `json:"ready_since_unix_nano,string"`
			Uptime    uint64 `json:"uptime_seconds,string"`
		}{v.Execution, v.ReadySinceUnixNano, v.UptimeSeconds})
	})
	router.Get(base+"/traffic", func(w http.ResponseWriter, r *http.Request) {
		values, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(values) > 0 && (len(values) != 1 || len(values["user_id"]) != 1) {
			httpapi.WriteError(w, fault.ErrInvalid)
			return
		}
		if values.Has("user_id") {
			v, err := s.GetTraffic(r.Context(), httpapi.ID(r), values.Get("user_id"))
			if err != nil {
				httpapi.WriteError(w, err)
				return
			}
			httpapi.WriteJSON(w, 200, entry{v.UserID, v.TX, v.RX})
			return
		}
		rows, err := s.ListTraffic(r.Context(), httpapi.ID(r))
		writeSnapshot(w, rows, "", err)
	})
	router.Post(base+"/traffic/resets", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" || r.ContentLength != 0 {
			httpapi.WriteError(w, fault.ErrInvalid)
			return
		}
		rows, err := s.ResetTraffic(r.Context(), httpapi.ID(r))
		writeSnapshot(w, rows, observations.ResetScope, err)
	})
	router.Get(base+"/online/users", func(w http.ResponseWriter, r *http.Request) {
		rows, err := s.ListOnlineUsers(r.Context(), httpapi.ID(r))
		if err != nil {
			httpapi.WriteError(w, err)
			return
		}
		users := make([]deviceCount, 0, len(rows))
		for _, v := range rows {
			users = append(users, deviceCount{v.UserID, v.Devices})
		}
		httpapi.WriteJSON(w, 200, struct {
			Users []deviceCount `json:"users"`
		}{users})
	})
	router.Post(base+"/online/disconnections", func(w http.ResponseWriter, r *http.Request) {
		body, err := httpapi.ReadBody(w, r, observations.MaxKickBodyBytes)
		if err != nil {
			httpapi.WriteError(w, err)
			return
		}
		input, err := disconnectInput(body)
		if err != nil {
			httpapi.WriteError(w, err)
			return
		}
		if err := s.DisconnectUsers(r.Context(), httpapi.ID(r), input); err != nil {
			httpapi.WriteError(w, err)
			return
		}
		httpapi.WriteJSON(w, 200, struct {
			Acknowledged bool `json:"acknowledged"`
		}{true})
	})
}

func disconnectInput(body []byte) ([]string, error) {
	// Unmarshal into a typed envelope and reject unknown/trailing fields.
	var input struct {
		IDs []string `json:"user_ids"`
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if d.Decode(&input) != nil || d.Decode(new(json.RawMessage)) != io.EOF {
		return nil, fault.ErrInvalid
	}
	return input.IDs, nil
}

func writeSnapshot(w http.ResponseWriter, rows []observations.Entry, scope string, err error) {
	if err != nil {
		httpapi.WriteError(w, err)
		return
	}
	res := snapshot{Entries: make([]entry, 0, len(rows)), ResetScope: scope}
	for _, v := range rows {
		res.Entries = append(res.Entries, entry{v.UserID, v.TX, v.RX})
	}
	httpapi.WriteJSON(w, 200, res)
}
