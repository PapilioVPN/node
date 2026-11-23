package runtime

import (
	"errors"
	"testing"

	"google.golang.org/grpc"
)

func TestRuntimeStatsRejectsReadsAtCapacity(t *testing.T) {
	service := New(availableInstance{})
	for range maxConcurrentReads {
		service.active <- struct{}{}
	}
	_, err := service.GetRuntimeStats(t.Context(), "primary")
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("GetRuntimeStats = %v, want ErrBusy", err)
	}
}

type availableInstance struct{}

func (availableInstance) NativeConnection(string) (*grpc.ClientConn, error) {
	return nil, nil
}
