package services_test

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/online/app/services"
	"github.com/PapilioVPN/node/internal/modules/xray/online/domain"
)

type onlineReader struct {
	count func(context.Context, domain.UserEmail) (domain.UserOnline, error)
	ips   func(context.Context, domain.UserEmail) (domain.UserOnlineIPs, error)
	users func(context.Context) (domain.OnlineUsers, error)
}

func (r onlineReader) ReadUserOnlineStats(ctx context.Context, email domain.UserEmail) (domain.UserOnline, error) {
	return r.count(ctx, email)
}
func (r onlineReader) ReadUserOnlineIPs(ctx context.Context, email domain.UserEmail) (domain.UserOnlineIPs, error) {
	return r.ips(ctx, email)
}
func (r onlineReader) ReadOnlineUsers(ctx context.Context) (domain.OnlineUsers, error) {
	return r.users(ctx)
}

func TestOnlineServiceSelectsOnlyExactImmutableBindingAndPreservesObservations(t *testing.T) {
	ctx := t.Context()
	observed := time.Now()
	countCalls, ipCalls, userCalls := 0, 0, 0
	const email domain.UserEmail = " Mixed>>>@Example "
	bindings := map[instances.InstanceID]services.OnlineReader{
		" edge ": onlineReader{
			count: func(received context.Context, e domain.UserEmail) (domain.UserOnline, error) {
				countCalls++
				if received != ctx || e != email {
					t.Fatal("count input changed")
				}
				return domain.UserOnline{OnlineIPCount: math.MaxInt64, ObservedAt: observed}, nil
			},
			ips: func(received context.Context, e domain.UserEmail) (domain.UserOnlineIPs, error) {
				ipCalls++
				if received != ctx || e != email {
					t.Fatal("IP input changed")
				}
				return domain.UserOnlineIPs{ObservedAt: observed, Items: []domain.OnlineIP{{IP: "z", LastSeenUnixSeconds: math.MinInt64}, {IP: "a", LastSeenUnixSeconds: math.MaxInt64}}}, nil
			},
			users: func(received context.Context) (domain.OnlineUsers, error) {
				userCalls++
				if received != ctx {
					t.Fatal("context changed")
				}
				return domain.OnlineUsers{ObservedAt: observed, Emails: []domain.UserEmail{"z", email, email, "   "}}, nil
			},
		},
		"disabled": nil,
		"other":    onlineReader{}, // Any accidental use fails; only the selected reader runs.
	}
	service := services.NewOnlineService(bindings)
	delete(bindings, " edge ")
	count, err := service.GetUserOnlineStats(ctx, " edge ", email)
	if err != nil || count.InstanceID != " edge " || count.Email != email || count.Online.OnlineIPCount != math.MaxInt64 || count.Online.ObservedAt != observed {
		t.Fatalf("count: %+v, %v", count, err)
	}
	ips, err := service.ListUserOnlineIPs(ctx, " edge ", email)
	if err != nil || ips.InstanceID != " edge " || ips.Email != email || ips.IPs.ObservedAt != observed || !reflect.DeepEqual(ips.IPs.Items, []domain.OnlineIP{{IP: "a", LastSeenUnixSeconds: math.MaxInt64}, {IP: "z", LastSeenUnixSeconds: math.MinInt64}}) {
		t.Fatalf("IPs: %+v, %v", ips, err)
	}
	users, err := service.ListOnlineUsers(ctx, " edge ")
	if err != nil || users.InstanceID != " edge " || users.Users.ObservedAt != observed || !reflect.DeepEqual(users.Users.Emails, []domain.UserEmail{"   ", email, email, "z"}) {
		t.Fatalf("users: %+v, %v", users, err)
	}
	for id, cause := range map[instances.InstanceID]error{"edge": instances.ErrNotFound, "unknown": instances.ErrNotFound, "disabled": domain.ErrAPIDisabled} {
		if v, err := service.GetUserOnlineStats(ctx, id, email); !errors.Is(err, cause) || v.InstanceID != "" {
			t.Fatalf("count selection: %+v, %v", v, err)
		}
		if v, err := service.ListUserOnlineIPs(ctx, id, email); !errors.Is(err, cause) || v.IPs.Items != nil {
			t.Fatalf("IP selection: %+v, %v", v, err)
		}
		if v, err := service.ListOnlineUsers(ctx, id); !errors.Is(err, cause) || v.Users.Emails != nil {
			t.Fatalf("users selection: %+v, %v", v, err)
		}
	}
	if countCalls != 1 || ipCalls != 1 || userCalls != 1 {
		t.Fatalf("native calls: count=%d IPs=%d users=%d", countCalls, ipCalls, userCalls)
	}
}

func TestOnlineServiceDiscardsPartialCanceledResultsAndPreservesEmptyObservations(t *testing.T) {
	for _, mode := range []string{"empty", "error", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			for _, operation := range []string{"count", "ips", "users"} {
				t.Run(operation, func(t *testing.T) {
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					calls := 0
					effect := func() error {
						calls++
						if mode == "cancel" {
							cancel()
						}
						if mode != "empty" {
							return domain.ErrOnlineObservationNotFound
						}
						return nil
					}
					service := services.NewOnlineService(map[instances.InstanceID]services.OnlineReader{"edge": onlineReader{
						count: func(context.Context, domain.UserEmail) (domain.UserOnline, error) {
							return domain.UserOnline{ObservedAt: time.Now()}, effect()
						},
						ips: func(context.Context, domain.UserEmail) (domain.UserOnlineIPs, error) {
							return domain.UserOnlineIPs{ObservedAt: time.Now()}, effect()
						},
						users: func(context.Context) (domain.OnlineUsers, error) {
							return domain.OnlineUsers{ObservedAt: time.Now()}, effect()
						},
					}})
					read := func() error {
						switch operation {
						case "count":
							v, err := service.GetUserOnlineStats(ctx, "edge", "reader")
							if err != nil && v != (domain.InstanceUserOnline{}) {
								t.Fatal("partial count")
							}
							if err == nil && (v.InstanceID != "edge" || v.Online.ObservedAt.IsZero()) {
								t.Fatal("empty count lost")
							}
							return err
						case "ips":
							v, err := service.ListUserOnlineIPs(ctx, "edge", "reader")
							if err != nil && (v.InstanceID != "" || v.IPs.Items != nil) {
								t.Fatal("partial IPs")
							}
							if err == nil && (v.InstanceID != "edge" || v.IPs.ObservedAt.IsZero()) {
								t.Fatal("empty IPs lost")
							}
							return err
						default:
							v, err := service.ListOnlineUsers(ctx, "edge")
							if err != nil && (v.InstanceID != "" || v.Users.Emails != nil) {
								t.Fatal("partial users")
							}
							if err == nil && (v.InstanceID != "edge" || v.Users.ObservedAt.IsZero()) {
								t.Fatal("empty users lost")
							}
							return err
						}
					}
					err := read()
					if mode == "empty" {
						if err != nil {
							t.Fatal(err)
						}
						return
					}
					if !errors.Is(err, domain.ErrOnlineObservationNotFound) || mode == "cancel" && !errors.Is(err, context.Canceled) {
						t.Fatalf("lost cause: %v", err)
					}
					cancel()
					if err := read(); !errors.Is(err, context.Canceled) || calls != 1 {
						t.Fatalf("canceled admission: %v calls=%d", err, calls)
					}
				})
			}
		})
	}
}
