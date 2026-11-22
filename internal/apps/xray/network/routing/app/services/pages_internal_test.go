package services

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/domain"
)

func TestExpiredObservationCannotContinue(t *testing.T) {
	s, err := New(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("a", 32)
	s.pages.items[id] = retained{page: domain.Page{ExpiresAt: time.Now().Add(-time.Second)}}
	if _, err := s.List(context.Background(), domain.PageRequest{InstanceID: "one", PageToken: id + ":50"}); !errors.Is(err, domain.ErrObservationExpired) {
		t.Fatal(err)
	}
	if len(s.pages.items) != 0 {
		t.Fatal("expired observation retained")
	}
}
