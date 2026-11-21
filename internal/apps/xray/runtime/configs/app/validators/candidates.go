// Package validators owns pure configuration input constraints.
package validators

import "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"

func Candidate(candidate domain.Candidate) error {
	switch candidate.Format {
	case domain.JSON, domain.YAML, domain.TOML, domain.Protobuf:
	default:
		return domain.ErrInvalidArgument
	}
	if len(candidate.Content) == 0 && candidate.Format != domain.Protobuf {
		return domain.ErrInvalidArgument
	}
	if len(candidate.Content) > domain.MaxCandidateBytes {
		return domain.ErrTooLarge
	}
	return nil
}
