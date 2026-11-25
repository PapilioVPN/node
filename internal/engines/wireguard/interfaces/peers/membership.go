package peers

import (
	"context"
	"net/netip"
	"time"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
)

type Member struct {
	PublicKey  [32]byte
	AllowedIPs []netip.Prefix
}

type MembershipReader interface {
	ReadMembership(context.Context, string) ([]Member, error)
}

type Membership struct {
	name   string
	reader MembershipReader
}

func NewMembership(name string, reader MembershipReader) *Membership {
	return &Membership{name: name, reader: reader}
}

func (service *Membership) List(ctx context.Context, name string) ([]Member, error) {
	if name == "" || name != service.name {
		return nil, status.ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return service.reader.ReadMembership(ctx, name)
}
