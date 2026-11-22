package validators

import (
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

func Inventory(after string, limit uint32) error {
	if len(after) > 128 || limit > 50 {
		return domain.ErrInvalidArgument
	}
	return nil
}
func Repair(r domain.RepairRequest) error {
	if r.MaxAttempts < 1 || r.MaxAttempts > 3 || len(r.ExecutionID) > 64 {
		return domain.ErrInvalidArgument
	}
	return nil
}
func TTL(seconds uint32) bool { return seconds >= 1 && seconds <= 86400 }
func AcquireLease(q domain.AcquireLease) error {
	if !TTL(q.TTLSeconds) || q.Owner == "" || len(q.Owner) > 128 || !utf8.ValidString(q.Owner) || len(q.ExecutionID) != 32 || (q.Policy != "keep_running" && q.Policy != "stop") {
		return domain.ErrInvalidArgument
	}
	for _, c := range q.Owner {
		if c < 32 || c == 127 {
			return domain.ErrInvalidArgument
		}
	}
	return nil
}
