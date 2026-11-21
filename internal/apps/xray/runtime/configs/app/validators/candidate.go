// Package validators checks candidate input without parsing native configuration.
package validators

import "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"

func Candidate(value domain.Candidate) error {
	switch value.Format {
	case domain.JSON, domain.YAML, domain.TOML, domain.Protobuf:
	default:
		return domain.ErrInvalidArgument
	}
	if len(value.Content) == 0 {
		return domain.ErrInvalidArgument
	}
	if len(value.Content) > domain.MaxCandidateBytes {
		return domain.ErrTooLarge
	}
	return nil
}
