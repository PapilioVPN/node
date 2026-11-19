//go:build integration && linux

package sys

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
)

func TestNativeNodeIdentityResourcesAndConcurrentSamples(t *testing.T) {
	reader := New()
	ctx := context.Background()
	info, err := reader.Info(ctx)
	if err != nil || info.PID != int32(os.Getpid()) || info.ParentPID != int32(os.Getppid()) || info.InitializedAt.IsZero() || info.GoVersion == "" {
		t.Fatalf("%+v %v", info, err)
	}
	resources, err := reader.Resources(ctx)
	if err != nil || resources.PID != info.PID || resources.Goroutines == 0 || resources.TicksPerSecond == 0 || resources.RuntimeMappedBytes < resources.HeapObjectsBytes {
		t.Fatalf("%+v %v", resources, err)
	}
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			for range 5 {
				result, err := reader.Resources(ctx)
				if err != nil || result.PID != info.PID {
					t.Errorf("%+v %v", result, err)
				}
			}
		})
	}
	group.Wait()
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := reader.Resources(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
