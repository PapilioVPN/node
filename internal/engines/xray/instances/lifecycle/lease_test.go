package lifecycle

import (
	"context"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/engines"
	"google.golang.org/grpc"
)

func TestManagementActivityPreventsExpiryOfAnActiveLease(t *testing.T) {
	service := newTestService(t, newTestChild(t), func(context.Context, grpc.ClientConnInterface) error { return nil })
	activity := engines.NewManagementActivity()
	service.WithManagementActivity(activity)
	if _, err := service.Start(t.Context(), "primary"); err != nil {
		t.Fatal(err)
	}
	expired := time.Now().Add(-time.Second)
	service.mu.Lock()
	service.run.keepAlive, service.run.expiresAt = time.Second, expired
	service.mu.Unlock()

	activity.Mark()
	service.expireLease()
	status, err := service.GetStatus("primary")
	if err != nil || status.State != Ready || !status.LeaseExpiresAt.After(expired) {
		t.Fatalf("extended execution = %+v, %v", status, err)
	}
	status, err = service.Stop(t.Context(), "primary")
	if err != nil || !status.LeaseExpiresAt.IsZero() {
		t.Fatalf("stopped status = %+v, %v", status, err)
	}
}
