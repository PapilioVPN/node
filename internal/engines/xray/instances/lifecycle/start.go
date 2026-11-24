package lifecycle

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"time"

	"github.com/PapilioVPN/node/internal/engines/xray/accounts/membership"
	"github.com/PapilioVPN/node/internal/engines/xray/configs/runconfig"
	"google.golang.org/grpc"
)

const (
	MaxInitialInbounds  = 32
	MaxExcludedInbounds = 1000
)

var ErrInvalidStart = errors.New("invalid xray start request")

type StartRequest struct {
	ID               string
	ConfigJSON       []byte // nil selects the configured file
	InitialUsers     []InboundUsers
	ExcludedInbounds []string
	KeepAliveSeconds *uint32 // nil preserves the current interval, zero disables it
}

type InboundUsers = membership.InboundSnapshot

type AccountInitializer = func(context.Context, string, *grpc.ClientConn, []InboundUsers, []string) (commit func(), err error)

type desiredConfig struct {
	source       []byte
	excluded     []string
	sourceSHA256 string
}

func (d desiredConfig) equal(other desiredConfig) bool {
	return bytes.Equal(d.source, other.source) && slices.Equal(d.excluded, other.excluded)
}

func (req StartRequest) selectUsers() ([]InboundUsers, error) {
	if len(req.ConfigJSON) > runconfig.MaxSourceBytes ||
		(req.ConfigJSON != nil && !runconfig.ValidJSONSource(req.ConfigJSON)) ||
		len(req.InitialUsers) > MaxInitialInbounds || len(req.ExcludedInbounds) > MaxExcludedInbounds {
		return nil, ErrInvalidStart
	}
	excluded := make(map[string]struct{}, len(req.ExcludedInbounds))
	for _, tag := range req.ExcludedInbounds {
		if !validInboundTag(tag) {
			return nil, ErrInvalidStart
		}
		excluded[tag] = struct{}{}
	}

	var selected []InboundUsers
	seen := make(map[string]struct{}, len(req.InitialUsers))
	total := 0
	for _, group := range req.InitialUsers {
		total += len(group.Accounts)
		if !validInboundTag(group.InboundTag) || group.Accounts == nil || total > membership.MaxSyncAccounts {
			return nil, ErrInvalidStart
		}
		if _, duplicate := seen[group.InboundTag]; duplicate {
			return nil, ErrInvalidStart
		}
		seen[group.InboundTag] = struct{}{}
		if _, skip := excluded[group.InboundTag]; !skip {
			selected = append(selected, group)
		}
	}
	return selected, nil
}

func validInboundTag(tag string) bool { return len(tag) > 0 && len(tag) <= 1024 }

func (service *Service) Start(ctx context.Context, id string) (Status, error) {
	return service.StartWith(ctx, StartRequest{ID: id})
}

// StartWith serializes execution changes, keeping the candidate private until
// its API and initial account snapshots have been acknowledged.
func (service *Service) StartWith(ctx context.Context, req StartRequest) (Status, error) {
	if err := service.admit(ctx, req.ID); err != nil {
		if !errors.Is(err, ErrNotFound) {
			status, _ := service.GetStatus(req.ID)
			return status, err
		}
		return Status{}, err
	}
	defer service.release()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	unlink := context.AfterFunc(service.shutdown, cancel)
	defer unlink()
	if service.shutdown.Err() != nil {
		cancel()
	}
	action, err := service.start(ctx, req)
	status, _ := service.GetStatus(req.ID)
	status.StartAction = action
	if err == nil && status.State != Ready {
		err = &StartError{Reason: ReadinessFailed, cause: ErrNotReady}
	}
	if err != nil && service.shutdown.Err() != nil && errors.Is(err, context.Canceled) {
		err = errors.Join(ErrUnavailable, err)
	}
	return status, err
}

func (service *Service) start(ctx context.Context, req StartRequest) (StartAction, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	users, err := req.selectUsers()
	if err != nil {
		return "", err
	}

	service.mu.RLock()
	run, state := service.run, service.state
	var keepAlive time.Duration
	if run != nil {
		keepAlive = run.keepAlive
	}
	service.mu.RUnlock()
	if state == Starting || state == Stopping {
		return "", ErrBusy
	}
	if req.KeepAliveSeconds != nil {
		keepAlive = time.Duration(*req.KeepAliveSeconds) * time.Second
	}

	source := req.ConfigJSON
	if source == nil {
		source, err = runconfig.ReadSource(service.config.ConfigFile, runconfig.MaxLocalSourceBytes)
		if err != nil {
			if run == nil {
				service.mu.Lock()
				service.state = Failed
				service.mu.Unlock()
			}
			return "", &StartError{Reason: LaunchFailed, cause: err}
		}
	}
	desired := desiredConfig{source: source, excluded: req.ExcludedInbounds}
	action := Started
	if state == Failed {
		action = Recovered
	}
	if run != nil {
		action, err = service.reconcile(ctx, run, desired, keepAlive)
		if err != nil || action == Heartbeat || action == LiveUpdated {
			return action, err
		}
		if err := ctx.Err(); err != nil {
			return action, err
		}
		if err := service.stop(ctx, Stopped); err != nil {
			return action, err
		}
	}
	return action, service.launchReady(ctx, desired.owned(), users, keepAlive)
}

func (d desiredConfig) owned() desiredConfig {
	d.source = bytes.Clone(d.source)
	d.excluded = slices.Clone(d.excluded)
	return d
}

func (d desiredConfig) confirmed() desiredConfig {
	sum := sha256.Sum256(d.source)
	d.sourceSHA256 = hex.EncodeToString(sum[:])
	return d
}
