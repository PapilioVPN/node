package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	native "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

// ReadUserOnlineStats performs one native observation, sharing channel and admission bounds.
func (c *Adapter) ReadUserOnlineStats(ctx context.Context, email domain.UserEmail) (domain.UserOnline, error) {
	if err := ctx.Err(); err != nil {
		return domain.UserOnline{}, err
	}
	ctx, done, err := c.API.Begin(ctx)
	if err != nil {
		return domain.UserOnline{}, err
	}
	defer done()
	client, err := c.API.StatsClient()
	if err != nil {
		return domain.UserOnline{}, err
	}
	name := "user>>>" + string(email) + ">>>online"
	response, err := client.GetStatsOnline(ctx, &native.GetStatsRequest{Name: name, Reset_: false})
	if cause := ctx.Err(); cause != nil {
		return domain.UserOnline{}, errors.Join(cause, err)
	}
	if err != nil {
		return domain.UserOnline{}, onlineReadError(err)
	}

	value := response.GetStat()
	if value == nil || value.Name != name || value.Value < 0 {
		return domain.UserOnline{}, domain.ErrInvalidOnlineResponse
	}
	return domain.UserOnline{OnlineIPCount: value.Value, ObservedAt: time.Now().UTC()}, nil
}

// ReadUserOnlineIPs performs one native observation, sharing channel and admission bounds.
func (c *Adapter) ReadUserOnlineIPs(ctx context.Context, email domain.UserEmail) (domain.UserOnlineIPs, error) {
	if err := ctx.Err(); err != nil {
		return domain.UserOnlineIPs{}, err
	}
	ctx, done, err := c.API.Begin(ctx)
	if err != nil {
		return domain.UserOnlineIPs{}, err
	}
	defer done()
	client, err := c.API.StatsClient()
	if err != nil {
		return domain.UserOnlineIPs{}, err
	}
	name := "user>>>" + string(email) + ">>>online"
	response, err := client.GetStatsOnlineIpList(ctx, &native.GetStatsRequest{Name: name, Reset_: false})
	if cause := ctx.Err(); cause != nil {
		return domain.UserOnlineIPs{}, errors.Join(cause, err)
	}
	if err != nil {
		return domain.UserOnlineIPs{}, onlineReadError(err)
	}

	if response == nil || response.Name != name {
		return domain.UserOnlineIPs{}, domain.ErrInvalidOnlineResponse
	}
	result := domain.UserOnlineIPs{Items: make([]domain.OnlineIP, 0, len(response.Ips))}
	for ip, lastSeen := range response.Ips {
		if err := ctx.Err(); err != nil {
			return domain.UserOnlineIPs{}, err
		}
		result.Items = append(result.Items, domain.OnlineIP{IP: ip, LastSeenUnixSeconds: lastSeen})
	}
	if err := ctx.Err(); err != nil {
		return domain.UserOnlineIPs{}, err
	}
	result.ObservedAt = time.Now().UTC()
	return result, nil
}

// ReadOnlineUsers performs one native observation, sharing channel and admission bounds.
func (c *Adapter) ReadOnlineUsers(ctx context.Context) (domain.OnlineUsers, error) {
	if err := ctx.Err(); err != nil {
		return domain.OnlineUsers{}, err
	}
	ctx, done, err := c.API.Begin(ctx)
	if err != nil {
		return domain.OnlineUsers{}, err
	}
	defer done()
	client, err := c.API.StatsClient()
	if err != nil {
		return domain.OnlineUsers{}, err
	}
	response, err := client.GetAllOnlineUsers(ctx, &native.GetAllOnlineUsersRequest{})
	if cause := ctx.Err(); cause != nil {
		return domain.OnlineUsers{}, errors.Join(cause, err)
	}
	if err != nil {
		return domain.OnlineUsers{}, onlineReadError(err)
	}

	if response == nil {
		return domain.OnlineUsers{}, domain.ErrInvalidOnlineResponse
	}
	result := domain.OnlineUsers{Emails: make([]domain.UserEmail, 0, len(response.Users))}
	for _, key := range response.Users {
		if err := ctx.Err(); err != nil {
			return domain.OnlineUsers{}, err
		}
		email, err := onlineUserEmail(key)
		if err != nil {
			return domain.OnlineUsers{}, err
		}
		result.Emails = append(result.Emails, email)
	}
	if err := ctx.Err(); err != nil {
		return domain.OnlineUsers{}, err
	}
	result.ObservedAt = time.Now().UTC()
	return result, nil
}

func onlineReadError(err error) error {
	switch status.Code(err) {
	case codes.NotFound:
		err = errors.Join(domain.ErrOnlineObservationNotFound, err)
	case codes.Unimplemented:
		err = errors.Join(domain.ErrOnlineUnsupported, err)
	case codes.ResourceExhausted:
		err = errors.Join(domain.ErrAPIResourceLimit, err)
	case codes.DeadlineExceeded:
		err = errors.Join(context.DeadlineExceeded, err)
	}
	return fmt.Errorf("read Xray online observation: %w", err)
}

// onlineUserEmail decodes the native key without splitting the opaque identity.
func onlineUserEmail(key string) (domain.UserEmail, error) {
	email, prefix := strings.CutPrefix(key, "user>>>")
	email, suffix := strings.CutSuffix(email, ">>>online")
	if !prefix || !suffix || email == "" {
		return "", domain.ErrInvalidOnlineResponse
	}
	return domain.UserEmail(email), nil
}
