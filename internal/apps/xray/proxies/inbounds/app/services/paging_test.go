package services_test

import (
	"context"
	"errors"
	"fmt"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/domain"
	"testing"
)

type catalogNative struct {
	native
	records []domain.Record
	calls   int
}

func (n *catalogNative) Records(context.Context) ([]domain.Record, error) {
	n.calls++
	return n.records, nil
}
func TestPagesFetchOncePreserveDuplicateOrderAndRejectGenerationChange(t *testing.T) {
	for _, count := range []int{0, 1, 50, 51, 1000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			n := &catalogNative{records: make([]domain.Record, count)}
			for i := range n.records {
				n.records[i] = domain.Record{Tag: fmt.Sprint(i % 3), Configuration: []byte{1, 2, 3}}
			}
			e := &execution{generation: 1}
			s, err := services.New([]services.Binding{{ID: "edge-a", Client: n}}, e)
			if err != nil {
				t.Fatal(err)
			}
			page, err := s.RecordPage(context.Background(), "edge-a", domain.PageRequest{})
			if err != nil {
				t.Fatal(err)
			}
			total := len(page.Inbounds)
			cursor := page.NextCursor
			for cursor != "" {
				p, err := s.RecordPage(context.Background(), "edge-a", domain.PageRequest{Cursor: cursor})
				if err != nil || len(p.Inbounds) > 50 {
					t.Fatal(p, err)
				}
				total += len(p.Inbounds)
				cursor = p.NextCursor
			}
			if total != count || n.calls != 1 {
				t.Fatal(total, n.calls)
			}
			if count > 50 {
				e.next()
				_, err = s.RecordPage(context.Background(), "edge-a", domain.PageRequest{Cursor: page.NextCursor})
				if !errors.Is(err, domain.ErrStale) {
					t.Fatal(err)
				}
			}
		})
	}
}
