package services_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/domain"
)

func TestPagesTraverseOneNativeObservation(t *testing.T) {
	calls := 0
	service, _ := services.New([]services.Binding{{ID: "a", Client: client(func(context.Context) ([]domain.Record, error) { calls++; return make([]domain.Record, 51), nil })}}, &execution{generation: 1})
	first, err := service.List(context.Background(), "a", domain.PageRequest{})
	if err != nil || len(first.Outbounds) != 50 || first.NextPageToken == "" {
		t.Fatal(first, err)
	}
	second, err := service.List(context.Background(), "a", domain.PageRequest{PageToken: first.NextPageToken})
	if err != nil || len(second.Outbounds) != 1 || calls != 1 || first.ObservedAt != second.ObservedAt {
		t.Fatal(second, err, calls)
	}
}

func TestPageSizesAndGenerationKeepBoundedStableRecords(t *testing.T) {
	for _, n := range []int{0, 1, 50, 51, 1000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			calls := 0
			e := &execution{generation: 1}
			s, _ := services.New([]services.Binding{{ID: "a", Client: client(func(context.Context) ([]domain.Record, error) { calls++; return make([]domain.Record, n), nil })}}, e)
			request := domain.PageRequest{}
			count := 0
			for {
				page, err := s.List(context.Background(), "a", request)
				if err != nil || len(page.Outbounds) > 50 || page.DefaultState != "unobservable" {
					t.Fatal(page, err)
				}
				count += len(page.Outbounds)
				request.PageToken = page.NextPageToken
				if request.PageToken == "" {
					break
				}
			}
			if count != n || calls != 1 {
				t.Fatal(count, calls)
			}
		})
	}
}
