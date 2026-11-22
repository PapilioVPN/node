package configuration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/domain"
	"github.com/xtls/xray-core/core"
	"google.golang.org/protobuf/proto"
)

type Inspector struct{}

// Inspect validates the native envelope, not handler construction or connectivity.
// Ignored expire/comment fields cannot become effective through replacement.
func (Inspector) Inspect(ctx context.Context, content []byte) (domain.Candidate, error) {
	if err := ctx.Err(); err != nil {
		return domain.Candidate{}, err
	}
	if err := validators.Configuration(content); err != nil {
		return domain.Candidate{}, err
	}
	var config core.OutboundHandlerConfig
	if err := proto.Unmarshal(content, &config); err != nil {
		return domain.Candidate{}, errors.Join(domain.ErrInvalidArgument, err)
	}
	if config.ProxySettings == nil || config.ProxySettings.Type == "" || (config.SenderSettings != nil && config.SenderSettings.Type == "") {
		return domain.Candidate{}, domain.ErrInvalidArgument
	}
	if err := validators.Tag(config.Tag); err != nil {
		return domain.Candidate{}, err
	}
	if config.Expire != 0 || config.Comment != "" {
		return domain.Candidate{}, domain.ErrUnsupported
	}
	canonical, err := (proto.MarshalOptions{Deterministic: true}).Marshal(&config)
	if err != nil {
		return domain.Candidate{}, err
	}
	digest := sha256.Sum256(canonical)
	return domain.Candidate{Tag: config.Tag, Fingerprint: hex.EncodeToString(digest[:])}, ctx.Err()
}
