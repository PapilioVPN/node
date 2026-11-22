package wire

import (
	"context"
	"errors"

	configs "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	accounts "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/domain"
)

func Code(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, domain.ErrInvalid), errors.Is(err, accounts.ErrInvalidArgument), errors.Is(err, configs.ErrInvalidArgument):
		return "invalid_argument"
	case errors.Is(err, domain.ErrCapacity), errors.Is(err, accounts.ErrCapacity), errors.Is(err, configs.ErrCapacity), errors.Is(err, configs.ErrTooLarge):
		return "resource_exhausted"
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, accounts.ErrNotFound), errors.Is(err, configs.ErrNotFound), errors.Is(err, configs.ErrNoApplication):
		return "not_found"
	case errors.Is(err, domain.ErrStale), errors.Is(err, domain.ErrConflict), errors.Is(err, configs.ErrConflict), errors.Is(err, configs.ErrStalePlan), errors.Is(err, configs.ErrPlanExpired), errors.Is(err, accounts.ErrStale), errors.Is(err, accounts.ErrExecutionChanged):
		return "conflict"
	case errors.Is(err, domain.ErrUnsupported), errors.Is(err, domain.ErrUncertain), errors.Is(err, accounts.ErrUncertain), errors.Is(err, accounts.ErrUnsupported), errors.Is(err, accounts.ErrNotRunning), errors.Is(err, configs.ErrUnsupported), errors.Is(err, configs.ErrNoDesired):
		return "failed_precondition"
	default:
		return "unavailable"
	}
}
