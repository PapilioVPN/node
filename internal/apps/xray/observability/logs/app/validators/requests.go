// Package validators checks log selectors and bounded read requests.
package validators

import "github.com/PapilioVPN/node/internal/apps/xray/observability/logs/domain"

func ID(id string) error {
	if len(id) == 0 || len(id) > 128 {
		return domain.ErrInvalidArgument
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return domain.ErrInvalidArgument
		}
	}
	return nil
}
func Limit(limit uint32) (int, error) {
	if limit > domain.MaxRecords {
		return 0, domain.ErrInvalidArgument
	}
	if limit == 0 {
		return domain.MaxRecords, nil
	}
	return int(limit), nil
}
