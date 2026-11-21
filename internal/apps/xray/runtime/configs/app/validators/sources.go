package validators

import (
	"path"
	"strings"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
)

func Sources(sources []domain.Source) error {
	if len(sources) == 0 {
		return domain.ErrInvalidArgument
	}
	if len(sources) > domain.MaxSources {
		return domain.ErrTooLarge
	}
	seen := make(map[string]bool, len(sources))
	total := 0
	for _, source := range sources {
		name := source.Name
		if name == "" || len(name) > 128 || !utf8.ValidString(name) || strings.ContainsAny(name, "/\\\x00") || seen[name] {
			return domain.ErrInvalidArgument
		}
		seen[name] = true
		protobuf := false
		switch strings.ToLower(path.Ext(name)) {
		case ".json", ".jsonc", ".yaml", ".yml", ".toml":
		case ".pb", ".protobuf":
			protobuf = true
			if len(sources) != 1 {
				return domain.ErrInvalidArgument
			}
		default:
			return domain.ErrInvalidArgument
		}
		if len(source.Content) == 0 && !protobuf {
			return domain.ErrInvalidArgument
		}
		if len(source.Content) > domain.MaxCandidateBytes-total {
			return domain.ErrTooLarge
		}
		total += len(source.Content)
	}
	return nil
}
