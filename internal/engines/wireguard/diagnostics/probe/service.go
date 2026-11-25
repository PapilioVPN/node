// Package probe measures reachability through a configured Linux interface.
package probe

import (
	"context"
	"errors"
	"net"
	"net/http"
	"syscall"
	"time"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
	"golang.org/x/sys/unix"
)

type Result struct {
	Reachable          bool      `json:"reachable"`
	HTTPStatus         int       `json:"http_status,omitempty"`
	ElapsedNanoseconds uint64    `json:"elapsed_nanoseconds,string"`
	ObservedAt         time.Time `json:"observed_at"`
}

type Service struct {
	target string
	ready  func(context.Context, string) error
	client *http.Client
	slots  chan struct{}
}

func New(target, device string, timeout time.Duration) *Service {
	service := &Service{target: target, slots: make(chan struct{}, 4)}
	if target == "" {
		return service
	}
	dialer := net.Dialer{Control: func(_, _ string, conn syscall.RawConn) error {
		var bindErr error
		err := conn.Control(func(fd uintptr) { bindErr = unix.BindToDevice(int(fd), device) })
		return errors.Join(err, bindErr)
	}}
	service.client = &http.Client{
		Timeout:       timeout,
		Transport:     &http.Transport{DialContext: dialer.DialContext, DisableKeepAlives: true, MaxResponseHeaderBytes: 32 << 10},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return service
}

func (service *Service) Bind(ready func(context.Context, string) error) {
	service.ready = ready
}

func (service *Service) Run(ctx context.Context, name string) (Result, error) {
	if service.client == nil || service.ready == nil {
		return Result{}, status.ErrUnavailable
	}
	select {
	case service.slots <- struct{}{}:
		defer func() { <-service.slots }()
	default:
		return Result{}, status.ErrBusy
	}
	if err := service.ready(ctx, name); err != nil {
		return Result{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, service.target, nil)
	if err != nil {
		return Result{}, status.ErrUnavailable
	}
	start := time.Now()
	reply, err := service.client.Do(request)
	result := Result{ElapsedNanoseconds: uint64(time.Since(start)), ObservedAt: time.Now().UTC()}
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return result, nil
	}
	_ = reply.Body.Close()
	result.Reachable, result.HTTPStatus = true, reply.StatusCode
	return result, nil
}
