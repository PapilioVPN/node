package services

import (
	"errors"
	"fmt"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/domain"
	"testing"
	"time"
)

func TestSnapshotExpiryAdmissionIsolationAndByteBudget(t *testing.T) {
	var store pageStore[int]
	load := func() ([]int, int, error) { return make([]int, 51), 51, nil }
	identity := func(i int) int { return i }
	_, page, _, _, err := store.page("a", domain.PageRequest{}, 1, 0, load, identity)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []domain.PageRequest{{Size: 51}, {Cursor: "invalid"}, {Cursor: page.NextCursor + "x"}} {
		if _, _, _, _, err := store.page("a", input, 1, 0, load, identity); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Fatal(input, err)
		}
	}
	for _, scope := range []struct {
		id                string
		generation, epoch uint64
	}{{"b", 1, 0}, {"a", 2, 0}, {"a", 1, 1}} {
		if _, _, _, _, err := store.page(scope.id, domain.PageRequest{Cursor: page.NextCursor}, scope.generation, scope.epoch, load, identity); !errors.Is(err, domain.ErrStale) {
			t.Fatal(scope, err)
		}
	}
	old := store.snapshots["a"]
	old.expires = time.Now().Add(-time.Second)
	store.snapshots["a"] = old
	if _, _, _, _, err := store.page("a", domain.PageRequest{Cursor: page.NextCursor}, 1, 0, load, identity); !errors.Is(err, domain.ErrStale) {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if _, _, _, _, err := store.page(fmt.Sprint(i), domain.PageRequest{}, 1, 0, load, identity); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, _, _, err := store.page("overload", domain.PageRequest{}, 1, 0, load, identity); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal(err)
	}
	var bounded pageStore[int]
	if _, _, _, _, err := bounded.page("large", domain.PageRequest{}, 1, 0, func() ([]int, int, error) { return make([]int, 51), domain.MaxResponseBytes + 1, nil }, identity); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal(err)
	}
}
