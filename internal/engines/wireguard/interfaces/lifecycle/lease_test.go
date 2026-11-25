package lifecycle

import (
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/engines"
)

func TestManagementActivityPreventsExpiryOfAnActiveLease(t *testing.T) {
	control := new(failedUpdateControl)
	activity := engines.NewManagementActivity()
	service := New("wg0", control, nil, nil).WithManagementActivity(activity)
	if _, err := service.Start(t.Context(), "wg0", []byte(validSource)); err != nil {
		t.Fatal(err)
	}
	expired := time.Now().Add(-time.Second)
	checkpoint := *service.applied.Load()
	checkpoint.LeaseExpiresAt, checkpoint.leaseWindow = expired, time.Second+leaseGrace
	service.applied.Store(&checkpoint)

	activity.Mark()
	if !service.expireLease(t.Context()) || control.removed || !service.LeaseExpiresAt(control.link).After(expired) {
		t.Fatal("active lease was not renewed")
	}
	if err := service.Stop(t.Context(), "wg0"); err != nil {
		t.Fatal(err)
	}
	if !service.LeaseExpiresAt(control.link).IsZero() {
		t.Fatal("stopped link still has a lease")
	}
}
