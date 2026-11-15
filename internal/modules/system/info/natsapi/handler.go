// Package natsapi presents node information to broker-authorized management clients.
package natsapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/PapilioVPN/node/internal/modules/system/info/domain"
	"github.com/PapilioVPN/node/internal/shared/natsresponse"
	"github.com/nats-io/nats.go"
)

type InfoReader interface {
	GetNodeInfo(context.Context) (domain.NodeInfo, error)
}
type Handler struct {
	Info             InfoReader
	MaxResponseBytes int
}

// GetNodeInfo consumes exactly one JSON object {} on <prefix>.node.info.get.
func (h Handler) GetNodeInfo(ctx context.Context, message *nats.Msg) error {
	var request *struct{}
	decoder := json.NewDecoder(bytes.NewReader(message.Data))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&request)
	if err == nil {
		var trailing any
		if decoder.Decode(&trailing) != io.EOF {
			err = errors.New("trailing JSON")
		}
	}
	if err != nil || request == nil {
		return natsresponse.Error(message, "invalid_argument", "expected an empty JSON object")
	}
	info, err := h.Info.GetNodeInfo(ctx)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return natsresponse.Error(message, "canceled", "request canceled")
		case errors.Is(err, context.DeadlineExceeded):
			return natsresponse.Error(message, "deadline_exceeded", "request deadline exceeded")
		default:
			return natsresponse.Error(message, "internal", "cannot read node information")
		}
	}
	return natsresponse.JSON(message, struct {
		NodeID       domain.NodeID       `json:"node_id"`
		Version      string              `json:"version"`
		APIVersion   string              `json:"api_version"`
		Capabilities []domain.Capability `json:"capabilities"`
	}{info.NodeID, info.Version, info.APIVersion, info.Capabilities}, h.MaxResponseBytes)
}
