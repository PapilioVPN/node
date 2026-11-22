package api

import (
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"google.golang.org/protobuf/encoding/protowire"
)

// Inspector projects only the native User.account TypedMessage type. Credential
// bytes are skipped, not copied or decoded. The normal Add boundary still decodes
// the complete official User exactly once. Repeated singular messages follow
// protobuf merge semantics, including a later account fragment without a type.
type Inspector struct{}

func (Inspector) AccountType(content []byte) (string, error) {
	name := ""
	for len(content) > 0 {
		number, kind, n := protowire.ConsumeTag(content)
		if n < 0 {
			return "", domain.ErrInvalidArgument
		}
		content = content[n:]
		size := protowire.ConsumeFieldValue(number, kind, content)
		if size < 0 {
			return "", domain.ErrInvalidArgument
		}
		if number == 3 {
			if kind != protowire.BytesType {
				return "", domain.ErrInvalidArgument
			}
			account, n := protowire.ConsumeBytes(content)
			if n < 0 {
				return "", domain.ErrInvalidArgument
			}
			for len(account) > 0 {
				field, wire, n := protowire.ConsumeTag(account)
				if n < 0 {
					return "", domain.ErrInvalidArgument
				}
				account = account[n:]
				length := protowire.ConsumeFieldValue(field, wire, account)
				if length < 0 {
					return "", domain.ErrInvalidArgument
				}
				if field == 1 {
					if wire != protowire.BytesType {
						return "", domain.ErrInvalidArgument
					}
					name, n = protowire.ConsumeString(account)
					if n < 0 || !utf8.ValidString(name) {
						return "", domain.ErrInvalidArgument
					}
				}
				account = account[length:]
			}
		}
		content = content[size:]
	}
	if name == "" || len(name) > 1024 {
		return "", domain.ErrInvalidArgument
	}
	return name, nil
}
