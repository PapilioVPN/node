package services_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"testing"
	"time"

	configs "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

type configurationCompiler func(context.Context, string, []configs.Source) (configs.Compilation, error)

func (c configurationCompiler) Compile(ctx context.Context, id string, sources []configs.Source) (configs.Compilation, error) {
	return c(ctx, id, sources)
}

func TestAccountConfigPreparationCompilesOnceForWholeCollectionAndHashesResult(t *testing.T) {
	for _, count := range []int{1, 50, 51, 1000} {
		calls := 0
		s, err := services.NewConfigurations(configurationCompiler(func(_ context.Context, id string, sources []configs.Source) (configs.Compilation, error) {
			calls++
			if id != "a" || len(sources) != 2 || sources[1].Name != "tail.json" {
				t.Fatal(id, sources)
			}
			return configs.Compilation{Content: []byte{1}, SHA256: "obsolete"}, nil
		}), func(_ context.Context, content []byte, sets []domain.InboundUserSet) ([]byte, error) {
			if !bytes.Equal(content, []byte{1}) || len(sets) != 1 || len(sets[0].Accounts) != count {
				t.Fatal(content, sets)
			}
			return []byte{2, 3}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		accounts := make([][]byte, count)
		for i := range accounts {
			accounts[i] = []byte{1}
		}
		result, err := s.Prepare(context.Background(), "a", []configs.Source{{Name: "base.json"}, {Name: "tail.json"}}, []domain.InboundUserSet{{InboundTag: "members", Accounts: accounts}})
		hash := sha256.Sum256([]byte{2, 3})
		if err != nil || calls != 1 || result.SHA256 != hex.EncodeToString(hash[:]) || result.InstanceID != "a" || result.PreparedAt.IsZero() {
			t.Fatal(result, calls, err)
		}
	}
}

func TestAccountConfigPreparationRejectsBeforeCompileAndNeverReturnsPartialOutput(t *testing.T) {
	calls := 0
	compiler := configurationCompiler(func(context.Context, string, []configs.Source) (configs.Compilation, error) {
		calls++
		return configs.Compilation{}, configs.ErrRejected
	})
	s, _ := services.NewConfigurations(compiler, func(context.Context, []byte, []domain.InboundUserSet) ([]byte, error) {
		t.Fatal("built rejected sources")
		return nil, nil
	})
	if _, err := s.Prepare(context.Background(), "a", nil, []domain.InboundUserSet{{InboundTag: ""}}); !errors.Is(err, domain.ErrInvalidArgument) || calls != 0 {
		t.Fatal(err, calls)
	}
	result, err := s.Prepare(context.Background(), "a", nil, []domain.InboundUserSet{{InboundTag: "members"}})
	if !errors.Is(err, domain.ErrInvalidArgument) || result.Content != nil || calls != 1 {
		t.Fatal(result, err, calls)
	}
}

func TestAccountConfigPreparationAdmissionIncludesReplacementAndJoinsCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	entered := make(chan struct{}, 2)
	s, _ := services.NewConfigurations(configurationCompiler(func(context.Context, string, []configs.Source) (configs.Compilation, error) {
		return configs.Compilation{}, nil
	}), func(ctx context.Context, _ []byte, _ []domain.InboundUserSet) ([]byte, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	})
	sets := []domain.InboundUserSet{{InboundTag: "members"}}
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() { _, _ = s.Prepare(ctx, "a", nil, sets) })
	}
	for range 2 {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if _, err := s.Prepare(context.Background(), "a", nil, sets); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal(err)
	}
	cancel()
	workers.Wait()
	if _, err := s.Prepare(ctx, "a", nil, sets); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
