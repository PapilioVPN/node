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
	var config core.OutboundHandlerConfig
	if err := proto.Unmarshal(content, &config); err != nil {
		return domain.Candidate{}, errors.Join(domain.ErrInvalidArgument, err)
	}
	_, candidate, err := inspectNative(ctx, &config)
	return candidate, err
}

func inspectNative(ctx context.Context, config *core.OutboundHandlerConfig) ([]byte, domain.Candidate, error) {
	if config.ProxySettings == nil || config.ProxySettings.Type == "" || (config.SenderSettings != nil && config.SenderSettings.Type == "") {
		return nil, domain.Candidate{}, domain.ErrInvalidArgument
	}
	if err := validators.Tag(config.Tag); err != nil {
		return nil, domain.Candidate{}, err
	}
	if config.Expire != 0 || config.Comment != "" {
		return nil, domain.Candidate{}, domain.ErrUnsupported
	}
	canonical, err := (proto.MarshalOptions{Deterministic: true}).Marshal(config)
	if err != nil {
		return nil, domain.Candidate{}, err
	}
	digest := sha256.Sum256(canonical)
	return canonical, domain.Candidate{Tag: config.Tag, Fingerprint: hex.EncodeToString(digest[:])}, ctx.Err()
}
