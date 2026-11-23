package native

import (
	"context"

	"github.com/PapilioVPN/node/internal/engines/xray/accounts/membership"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

const (
	vlessInboundType = "xray.proxy.vless.inbound.Config"
	vlessAccountType = "xray.proxy.vless.Account"
	maxInbounds      = 1024
	maxAccounts      = 65536
)

type inboundAccounts struct {
	layout        accountLayout
	ss2022KeySize int
	ambiguous     bool

	// The mutation lock protects the live membership and the outcome fence.
	names     map[string]membership.Key
	records   map[string]*User
	ids       map[membership.IDKey]struct{}
	uncertain bool
}

type inventory struct {
	conn     *grpc.ClientConn
	inbounds map[string]*inboundAccounts
	size     int // Protected by the mutation lock after publication.
}

func (c *Client) inventory(ctx context.Context, instanceID string) (*inventory, error) {
	conn, err := c.connection(instanceID)
	if err != nil {
		return nil, err
	}
	if cached := c.cached.Load(); cached != nil && cached.conn == conn {
		return cached, nil
	}
	if err := c.loading.Acquire(ctx, 1); err != nil {
		return nil, err
	}
	defer c.loading.Release(1)
	if cached := c.cached.Load(); cached != nil && cached.conn == conn {
		return cached, nil
	}
	if !c.current(instanceID, conn) {
		return nil, membership.ErrUnavailable
	}

	var response ListInboundsResponse
	err = conn.Invoke(ctx, listInbounds, &ListInboundsRequest{}, &response,
		grpc.StaticMethod(), grpc.MaxCallRecvMsgSize(16<<20))
	if err := c.readResult(ctx, instanceID, conn, err); err != nil {
		return nil, err
	}
	result, err := buildInventory(conn, response.Inbounds)
	if err != nil {
		return nil, err
	}
	if !c.current(instanceID, conn) {
		return nil, membership.ErrUnavailable
	}
	c.cached.Store(result)
	return result, nil
}

func buildInventory(conn *grpc.ClientConn, inbounds []*Inbound) (*inventory, error) {
	if len(inbounds) > maxInbounds {
		return nil, membership.ErrTooLarge
	}
	result := &inventory{conn: conn, inbounds: make(map[string]*inboundAccounts, len(inbounds))}
	for _, raw := range inbounds {
		tag := raw.GetTag()
		if tag == "" {
			continue // Xray's exact handler selection requires a tag.
		}
		if _, exists := result.inbounds[tag]; exists {
			return nil, membership.ErrUnavailable
		}
		entry := new(inboundAccounts)
		result.inbounds[tag] = entry
		settings := raw.GetProxySettings()
		entry.layout = accountLayouts[settings.GetType()]
		if entry.layout.protocol == "" {
			continue
		}
		users, valid := entry.configuredUsers(settings.GetValue())
		if !valid {
			entry.ambiguous = true
			continue
		}
		result.size += len(users)
		if result.size > maxAccounts {
			return nil, membership.ErrTooLarge
		}
		entry.names = make(map[string]membership.Key, len(users))
		entry.records = make(map[string]*User, len(users))
		entry.ids = make(map[membership.IDKey]struct{}, len(users))
		for _, user := range users {
			wire := user.GetAccount()
			name, nameErr := entry.name(user.GetEmail())
			key, keyErr := entry.key(name, wire.GetValue())
			if nameErr != nil || wire.GetType() != entry.layout.accountType || keyErr != nil || !entry.insert(name, key) {
				entry.ambiguous = true
				break
			}
			if name != "" {
				entry.records[name] = user
			}
		}
	}
	return result, nil
}

func (entry *inboundAccounts) configuredUsers(data []byte) ([]*User, bool) {
	var users []*User
	for len(data) > 0 {
		number, kind, tagLen := protowire.ConsumeTag(data)
		if tagLen < 0 {
			return nil, false
		}
		data = data[tagLen:]
		fieldLen := protowire.ConsumeFieldValue(number, kind, data)
		if fieldLen < 0 {
			return nil, false
		}
		switch {
		case int(number) == entry.layout.usersField:
			if kind != protowire.BytesType || len(users) == maxAccounts {
				return nil, false
			}
			raw, _ := protowire.ConsumeBytes(data)
			user := new(User)
			if proto.Unmarshal(raw, user) != nil {
				return nil, false
			}
			users = append(users, user)
		case number == 1 && entry.layout.protocol == membership.SS2022 && kind == protowire.BytesType:
			method, _ := protowire.ConsumeString(data)
			switch method {
			case "2022-blake3-aes-128-gcm":
				entry.ss2022KeySize = 16
			case "2022-blake3-aes-256-gcm":
				entry.ss2022KeySize = 32
			}
		case number == 8 && entry.layout.protocol == membership.WireGuard && kind == protowire.VarintType:
			client, _ := protowire.ConsumeVarint(data)
			if client != 0 {
				entry.layout = accountLayout{}
				return nil, true
			}
		}
		data = data[fieldLen:]
	}
	return users, entry.layout.protocol != membership.SS2022 || entry.ss2022KeySize != 0
}

func (i *inventory) selectInbound(tag string, vlessOnly bool) (*inboundAccounts, error) {
	entry, exists := i.inbounds[tag]
	if !exists {
		return nil, membership.ErrInboundMissing
	}
	if entry.layout.protocol == "" || (vlessOnly && entry.layout.protocol != membership.VLESS) {
		return nil, membership.ErrProtocol
	}
	return entry, nil
}

func (entry *inboundAccounts) insert(name string, key membership.Key) bool {
	if _, exists := entry.ids[key.Identity]; exists {
		return false
	}
	if name != "" {
		if _, exists := entry.names[name]; exists {
			return false
		}
		entry.names[name] = key
	}
	entry.ids[key.Identity] = struct{}{}
	return true
}
