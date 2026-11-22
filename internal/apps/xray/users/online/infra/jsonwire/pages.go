package jsonwire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"time"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/xray/users/online/domain"
)

// DecodePage validates the collection envelope once, retaining explicit field
// presence for targeted IPs. Zero page_size selects the documented default.
func DecodePage(data []byte, ips bool) (r domain.PageRequest, err error) {
	if len(data) > 32<<10 {
		return r, domain.ErrCapacity
	}
	if !utf8.Valid(data) {
		return r, domain.ErrInvalidArgument
	}
	d := json.NewDecoder(bytes.NewReader(data))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return r, domain.ErrInvalidArgument
	}
	seen := map[string]bool{}
	for d.More() {
		token, err := d.Token()
		name, ok := token.(string)
		if err != nil || !ok || seen[name] {
			return r, domain.ErrInvalidArgument
		}
		seen[name] = true
		if name == "page_size" {
			var size *int
			if err := d.Decode(&size); err != nil || size == nil {
				return r, domain.ErrInvalidArgument
			}
			r.PageSize = *size
			continue
		}
		if name != "instance_id" && name != "page_token" && !(ips && name == "email") {
			return r, domain.ErrInvalidArgument
		}
		var value *string
		if err := d.Decode(&value); err != nil || value == nil {
			return r, domain.ErrInvalidArgument
		}
		switch name {
		case "instance_id":
			r.InstanceID = *value
		case "email":
			r.Email = *value
		case "page_token":
			r.PageToken = *value
		}
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') {
		return r, domain.ErrInvalidArgument
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) || !seen["instance_id"] || (ips && !seen["email"]) {
		return r, domain.ErrInvalidArgument
	}
	return r, nil
}

type pageMetadata struct {
	ObservationID string    `json:"observation_id"`
	ExpiresAt     time.Time `json:"expires_at"`
	ServedAt      time.Time `json:"served_at"`
	Fresh         bool      `json:"fresh"`
	Total         int       `json:"total"`
	NextPageToken string    `json:"next_page_token"`
}

func paging(p domain.Page) pageMetadata {
	return pageMetadata{p.ObservationID, p.ExpiresAt, p.ServedAt, p.Fresh, p.Total, p.NextPageToken}
}
func pageObservation(p domain.Page) observation {
	o := metadata(p.Observation)
	o.Source = p.Source
	return o
}
func EncodePage(p domain.Page) ([]byte, error) {
	type record struct {
		Email    string `json:"email"`
		IP       string `json:"ip,omitempty"`
		LastSeen *int64 `json:"last_seen_unix_seconds,omitempty,string"`
	}
	rows := make([]record, len(p.Records))
	for i, row := range p.Records {
		rows[i].Email = row.Email
		if row.IP != nil {
			rows[i].IP = row.IP.Address
			rows[i].LastSeen = &row.IP.LastSeenUnixSeconds
		}
	}
	return json.Marshal(struct {
		Observation observation  `json:"observation"`
		Page        pageMetadata `json:"page"`
		Records     []record     `json:"records"`
	}{pageObservation(p), paging(p), rows})
}
func EncodeIPPage(p domain.Page) ([]byte, error) {
	type ip struct {
		Address  string `json:"ip"`
		LastSeen int64  `json:"last_seen_unix_seconds,string"`
	}
	var rows []ip
	if p.State != domain.Untracked {
		rows = make([]ip, len(p.Records))
		for i, row := range p.Records {
			rows[i] = ip{row.IP.Address, row.IP.LastSeenUnixSeconds}
		}
	}
	return json.Marshal(struct {
		Observation observation  `json:"observation"`
		State       domain.State `json:"state"`
		IPs         []ip         `json:"ips"`
		Page        pageMetadata `json:"page"`
	}{pageObservation(p), p.State, rows, paging(p)})
}
