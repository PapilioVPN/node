package validators

import "github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/domain"

func Replacement(request domain.ReplacementRequest) error {
	if err := Tag(request.Tag); err != nil {
		return err
	}
	if err := Configuration(request.Configuration); err != nil {
		return err
	}
	return Policies(request.DefaultPolicy, request.ConnectionPolicy)
}
func Policies(defaultPolicy domain.DefaultPolicy, connections domain.ConnectionPolicy) error {
	switch defaultPolicy {
	case domain.PreserveDefault, domain.MayChangeDefault:
	default:
		return domain.ErrInvalidArgument
	}
	switch connections {
	case domain.RetainConnections, domain.TerminateConnections:
	default:
		return domain.ErrInvalidArgument
	}
	return nil
}

func PlanID(value string) error {
	if len(value) != 32 {
		return domain.ErrInvalidArgument
	}
	for _, b := range []byte(value) {
		if !((b >= '0' && b <= '9') || (b >= 'a' && b <= 'f')) {
			return domain.ErrInvalidArgument
		}
	}
	return nil
}
