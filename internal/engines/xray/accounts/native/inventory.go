package native

import (
	"context"

	"github.com/PapilioVPN/node/internal/engines/xray/accounts/membership"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

const (
	vlessInboundType = "xray.proxy.vless.inbound.Config"
	vlessAccountType = "xray.proxy.vless.Account"
	maxInbounds      = 1024
	maxAccounts      = 65536
)

type inboundAccounts struct {
	vless     bool
	ambiguous bool

	// The mutation lock protects the live membership and the outcome fence.
	names     map[string]membership.IDKey
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
		if settings.GetType() != vlessInboundType {
			continue
		}
		entry.vless = true

		var config VlessInbound
		decoder := proto.UnmarshalOptions{DiscardUnknown: true}
		if err := decoder.Unmarshal(settings.Value, &config); err != nil {
			entry.ambiguous = true
			continue
		}
		result.size += len(config.Users)
		if result.size > maxAccounts {
			return nil, membership.ErrTooLarge
		}
		entry.names = make(map[string]membership.IDKey, len(config.Users))
		entry.ids = make(map[membership.IDKey]struct{}, len(config.Users))
		for _, user := range config.Users {
			var account VlessAccount
			wire := user.GetAccount()
			if wire.GetType() != vlessAccountType || decoder.Unmarshal(wire.Value, &account) != nil {
				entry.ambiguous = true
				break
			}
			id, valid := membership.ParseUUID(account.Id)
			if !valid || !entry.insert(user.GetEmail(), id.Key()) {
				entry.ambiguous = true
				break
			}
		}
	}
	return result, nil
}

func (i *inventory) vless(tag string) (*inboundAccounts, error) {
	entry, exists := i.inbounds[tag]
	if !exists {
		return nil, membership.ErrInboundMissing
	}
	if !entry.vless {
		return nil, membership.ErrProtocol
	}
	return entry, nil
}

func (entry *inboundAccounts) insert(email string, id membership.IDKey) bool {
	if _, exists := entry.ids[id]; exists {
		return false
	}
	if email != "" {
		name := membership.NameKey(email)
		if _, exists := entry.names[name]; exists {
			return false
		}
		entry.names[name] = id
	}
	entry.ids[id] = struct{}{}
	return true
}
