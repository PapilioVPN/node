package configuration

import (
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"google.golang.org/protobuf/encoding/protowire"
)

// Empty SS2022 JSON compiles to ServerConfig, whose field layout differs from
// MultiUserServerConfig. Convert only its supported AES settings; do not lose
// unknown fields across this type change or promote ChaCha/relay configurations.
func multiUserShadowsocks(content []byte) ([]byte, error) {
	var output []byte
	method := ""
	for len(content) != 0 {
		number, kind, n := protowire.ConsumeTag(content)
		if n < 0 {
			return nil, domain.ErrInvalidArgument
		}
		size := protowire.ConsumeFieldValue(number, kind, content[n:])
		if size < 0 {
			return nil, domain.ErrInvalidArgument
		}
		value := content[n : n+size]
		switch number {
		case 1, 2:
			if kind != protowire.BytesType {
				return nil, domain.ErrInvalidArgument
			}
			if number == 1 {
				method, _ = protowire.ConsumeString(value)
			}
			output = append(output, content[:n+size]...)
		case 3:
			if kind != protowire.BytesType {
				return nil, domain.ErrInvalidArgument
			}
		case 4:
			if kind != protowire.VarintType {
				return nil, domain.ErrInvalidArgument
			}
		case 5:
			if kind != protowire.BytesType && kind != protowire.VarintType {
				return nil, domain.ErrInvalidArgument
			}
			output = protowire.AppendTag(output, 4, kind)
			output = append(output, value...)
		default:
			return nil, domain.ErrUnsupported
		}
		content = content[n+size:]
	}
	if method != "2022-blake3-aes-128-gcm" && method != "2022-blake3-aes-256-gcm" {
		return nil, domain.ErrUnsupported
	}
	return output, nil
}
