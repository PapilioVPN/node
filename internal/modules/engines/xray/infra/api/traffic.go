package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	native "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

// ReadTrafficCounter makes one exact GetStats read, sharing runtime API capacity.
// Selection is already validated by the caller's input boundary. Reset stays false.
func (c *Adapter) ReadTrafficCounter(ctx context.Context, selection domain.TrafficSelection) (domain.TrafficCounter, error) {
	if err := ctx.Err(); err != nil {
		return domain.TrafficCounter{}, err
	}
	ctx, done, err := c.API.Begin(ctx)
	if err != nil {
		return domain.TrafficCounter{}, err
	}
	defer done()
	client, err := c.API.StatsClient()
	if err != nil {
		return domain.TrafficCounter{}, err
	}
	name := trafficCounterName(selection)
	response, err := client.GetStats(ctx, &native.GetStatsRequest{Name: name, Reset_: false})
	if cause := ctx.Err(); cause != nil {
		return domain.TrafficCounter{}, errors.Join(cause, err)
	}
	if err != nil {
		switch status.Code(err) {
		case codes.NotFound:
			err = errors.Join(domain.ErrTrafficCounterNotFound, err)
		case codes.Unimplemented:
			err = errors.Join(domain.ErrTrafficUnsupported, err)
		case codes.ResourceExhausted:
			err = errors.Join(domain.ErrAPIResourceLimit, err)
		case codes.DeadlineExceeded:
			err = errors.Join(context.DeadlineExceeded, err)
		}
		return domain.TrafficCounter{}, fmt.Errorf("read Xray traffic counter: %w", err)
	}
	value := response.GetStat()
	if value == nil || value.Name != name {
		return domain.TrafficCounter{}, domain.ErrInvalidTrafficResponse
	}
	return domain.TrafficCounter{Bytes: value.Value, ObservedAt: time.Now().UTC()}, nil
}

func trafficCounterName(selection domain.TrafficSelection) string {
	return string(selection.Scope) + ">>>" + selection.Name + ">>>traffic>>>" + string(selection.Direction)
}
