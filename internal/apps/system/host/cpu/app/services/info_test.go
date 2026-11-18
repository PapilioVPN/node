package services_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
)

func TestInfoUsesOneReadForGrowingInventories(t *testing.T) {
	for _, count := range []int{1, 64, 4096} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		calls := 0
		service := services.Service{InfoReader: infoReaderFunc(func(received context.Context) ([]domain.Processor, error) {
			if received != ctx {
				t.Fatal("lost caller context")
			}
			calls++
			rows := make([]domain.Processor, count)
			for i := range rows {
				rows[i].ID = uint32(i)
			}
			return rows, nil
		})}
		started := time.Now()
		got, err := service.ReadInfo(ctx)
		cancel()
		if err != nil || calls != 1 || got.Info.LogicalCount != uint32(count) || len(got.Info.Processors) != count || got.Info.PackageCount != nil || got.Info.PhysicalCoreCount != nil || got.ObservedAt.Before(started) || got.ObservedAt.After(time.Now()) || got.Duration < 0 || got.Duration > time.Since(started) {
			t.Fatalf("inventory count %d: %+v %v; calls=%d", count, got, err, calls)
		}
	}
}

func TestInfoFailurePreservesCauseAndDropsPartialRows(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, errors.New("private cpuinfo cause")} {
		service := services.Service{InfoReader: infoReaderFunc(func(context.Context) ([]domain.Processor, error) { return []domain.Processor{{ID: 1}}, cause })}
		got, err := service.ReadInfo(context.Background())
		if !errors.Is(err, cause) || got.Info.Processors != nil || !got.ObservedAt.IsZero() {
			t.Fatalf("partial success or lost cause: %+v %v", got, err)
		}
	}
}
