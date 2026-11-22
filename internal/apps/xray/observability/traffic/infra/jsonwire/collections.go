package jsonwire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/domain"
)

// Fields checks object shape once, before typed operation decoding. Integer64
// inputs use canonical decimal strings; bounded page sizes/counts are numbers.
func Fields(data []byte, allowed ...string) (map[string]json.RawMessage, error) {
	if len(data) > MaxRequestBytes || !utf8.Valid(data) {
		return nil, domain.ErrInvalidArgument
	}
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, domain.ErrInvalidArgument
	}
	result := make(map[string]json.RawMessage)
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return nil, domain.ErrInvalidArgument
		}
		name, ok := key.(string)
		if !ok {
			return nil, domain.ErrInvalidArgument
		}
		found := false
		for _, a := range allowed {
			found = found || a == name
		}
		if !found || result[name] != nil {
			return nil, domain.ErrInvalidArgument
		}
		var value json.RawMessage
		if d.Decode(&value) != nil || bytes.Equal(value, []byte("null")) {
			return nil, domain.ErrInvalidArgument
		}
		result[name] = value
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') {
		return nil, domain.ErrInvalidArgument
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) {
		return nil, domain.ErrInvalidArgument
	}
	return result, nil
}
func textField(f map[string]json.RawMessage, key string, required bool) (string, error) {
	v, ok := f[key]
	if !ok {
		if required {
			return "", domain.ErrInvalidArgument
		}
		return "", nil
	}
	var result string
	if json.Unmarshal(v, &result) != nil {
		return "", domain.ErrInvalidArgument
	}
	return result, nil
}
func countField(f map[string]json.RawMessage, key string) (uint32, error) {
	v, ok := f[key]
	if !ok {
		return 0, nil
	}
	var result uint32
	if json.Unmarshal(v, &result) != nil {
		return 0, domain.ErrInvalidArgument
	}
	return result, nil
}
func revisionField(f map[string]json.RawMessage, key string, required bool) (uint64, error) {
	v, err := textField(f, key, required)
	if err != nil {
		return 0, err
	}
	if !required && f[key] == nil {
		return 0, nil
	}
	result, err := strconv.ParseUint(v, 10, 64)
	if err != nil || strconv.FormatUint(result, 10) != v {
		return 0, domain.ErrInvalidArgument
	}
	return result, nil
}
func DecodePage(data []byte) (string, domain.PageRequest, error) {
	f, err := Fields(data, "instance_id", "scope", "page_size", "page_token")
	if err != nil {
		return "", domain.PageRequest{}, err
	}
	id, e1 := textField(f, "instance_id", true)
	scope, e2 := textField(f, "scope", true)
	token, e3 := textField(f, "page_token", false)
	size, e4 := countField(f, "page_size")
	return id, domain.PageRequest{Scope: domain.Scope(scope), PageSize: size, PageToken: token}, errors.Join(e1, e2, e3, e4)
}
func DecodePair(data []byte) (string, domain.Scope, string, error) {
	f, err := Fields(data, "instance_id", "scope", "name")
	if err != nil {
		return "", "", "", err
	}
	id, e1 := textField(f, "instance_id", true)
	scope, e2 := textField(f, "scope", true)
	name, e3 := textField(f, "name", true)
	return id, domain.Scope(scope), name, errors.Join(e1, e2, e3)
}
func DecodeReset(data []byte, kind domain.ResetKind) (string, domain.ResetRequest, error) {
	allowed := []string{"instance_id", "epoch", "operation_id", "expected_revision", "scope"}
	if kind == domain.ResetScope {
		allowed = append(allowed, "max_counters")
	} else {
		allowed = append(allowed, "name")
		if kind == domain.ResetExact {
			allowed = append(allowed, "direction")
		}
	}
	f, err := Fields(data, allowed...)
	if err != nil {
		return "", domain.ResetRequest{}, err
	}
	id, e1 := textField(f, "instance_id", true)
	epoch, e2 := textField(f, "epoch", true)
	op, e3 := textField(f, "operation_id", true)
	revision, e4 := revisionField(f, "expected_revision", true)
	scope, e5 := textField(f, "scope", true)
	name, e6 := textField(f, "name", kind != domain.ResetScope)
	direction, e7 := textField(f, "direction", kind == domain.ResetExact)
	maximum, e8 := countField(f, "max_counters")
	return id, domain.ResetRequest{Epoch: epoch, OperationID: op, ExpectedRevision: revision, Kind: kind, Selection: domain.Selection{Scope: domain.Scope(scope), Name: name, Direction: domain.Direction(direction)}, MaxCounters: maximum}, errors.Join(e1, e2, e3, e4, e5, e6, e7, e8)
}
func DecodeStatus(data []byte) (string, uint64, uint32, error) {
	f, err := Fields(data, "instance_id", "revision", "offset")
	if err != nil {
		return "", 0, 0, err
	}
	id, e1 := textField(f, "instance_id", true)
	revision, e2 := revisionField(f, "revision", false)
	offset, e3 := countField(f, "offset")
	return id, revision, offset, errors.Join(e1, e2, e3)
}

type counter struct {
	Name      string           `json:"name"`
	Direction domain.Direction `json:"direction"`
	Bytes     int64            `json:"bytes,string"`
}

func EncodePage(r domain.Page) ([]byte, error) {
	records := make([]counter, 0, len(r.Counters))
	for _, item := range r.Counters {
		records = append(records, counter{item.Selection.Name, item.Selection.Direction, item.Bytes})
	}
	return json.Marshal(struct {
		InstanceID    string       `json:"instance_id"`
		Generation    uint64       `json:"generation,string"`
		Scope         domain.Scope `json:"scope"`
		Counters      []counter    `json:"counters"`
		Source        string       `json:"source"`
		ObservedAt    time.Time    `json:"observed_at"`
		DurationNS    int64        `json:"duration_ns,string"`
		NextPageToken string       `json:"next_page_token"`
	}{r.InstanceID, r.Generation, r.Scope, records, r.Source, r.ObservedAt, int64(r.Duration), r.NextPageToken})
}
func EncodePair(r domain.Pair) ([]byte, error) {
	var uplink, downlink json.RawMessage
	var err error
	if r.Uplink != nil {
		uplink, err = Encode(*r.Uplink)
		if err != nil {
			return nil, err
		}
	}
	if r.Downlink != nil {
		downlink, err = Encode(*r.Downlink)
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(struct {
		InstanceID string          `json:"instance_id"`
		Generation uint64          `json:"generation,string"`
		Scope      domain.Scope    `json:"scope"`
		Name       string          `json:"name"`
		Uplink     json.RawMessage `json:"uplink"`
		Downlink   json.RawMessage `json:"downlink"`
		Source     string          `json:"source"`
		ObservedAt time.Time       `json:"observed_at"`
		DurationNS int64           `json:"duration_ns,string"`
	}{r.InstanceID, r.Generation, r.Scope, r.Name, uplink, downlink, r.Source, r.ObservedAt, int64(r.Duration)})
}
func EncodeReset(r domain.ResetReport) ([]byte, error) {
	type item struct {
		Name           string           `json:"name"`
		Direction      domain.Direction `json:"direction"`
		Attempted      bool             `json:"attempted"`
		Effect         domain.Effect    `json:"effect"`
		PreviousBytes  *int64           `json:"previous_bytes,string"`
		AcknowledgedAt *time.Time       `json:"acknowledged_at"`
		Error          string           `json:"error"`
	}
	items := make([]item, 0, len(r.Items))
	for _, v := range r.Items {
		items = append(items, item{v.Selection.Name, v.Selection.Direction, v.Attempted, v.Effect, v.PreviousBytes, v.AcknowledgedAt, v.Error})
	}
	return json.Marshal(struct {
		InstanceID       string           `json:"instance_id"`
		Epoch            string           `json:"epoch"`
		OperationID      string           `json:"operation_id"`
		Revision         uint64           `json:"revision,string"`
		Generation       uint64           `json:"generation,string"`
		Kind             domain.ResetKind `json:"kind"`
		Scope            domain.Scope     `json:"scope"`
		Name             string           `json:"name"`
		Direction        domain.Direction `json:"direction"`
		Outcome          string           `json:"outcome"`
		Error            string           `json:"error"`
		RecoveryRequired bool             `json:"recovery_required"`
		StartedAt        time.Time        `json:"started_at"`
		CompletedAt      *time.Time       `json:"completed_at"`
		Items            []item           `json:"items"`
		Total            uint32           `json:"total"`
		NextOffset       uint32           `json:"next_offset"`
	}{r.InstanceID, r.Epoch, r.OperationID, r.Revision, r.Generation, r.Kind, r.Scope, r.Name, r.Direction, r.Outcome, r.Error, r.RecoveryRequired, r.StartedAt, r.CompletedAt, items, r.Total, r.NextOffset})
}
