package native

import (
	"context"
	"crypto/sha256"
	"strings"

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

type fingerprint [sha256.Size]byte

type inboundAccounts struct {
	vless     bool
	ambiguous bool

	// The mutation lock protects the live membership and the outcome fence.
	names     map[fingerprint]fingerprint
	ids       map[fingerprint]struct{}
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
		entry.names = make(map[fingerprint]fingerprint, len(config.Users))
		entry.ids = make(map[fingerprint]struct{}, len(config.Users))
		for _, user := range config.Users {
			var account VlessAccount
			wire := user.GetAccount()
			if wire.GetType() != vlessAccountType || decoder.Unmarshal(wire.Value, &account) != nil {
				entry.ambiguous = true
				break
			}
			id, valid := membership.ParseUUID(account.Id)
			if !valid || !entry.insert(user.GetEmail(), effectiveID(id)) {
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

func (entry *inboundAccounts) insert(email string, id fingerprint) bool {
	if _, exists := entry.ids[id]; exists {
		return false
	}
	if email != "" {
		name := emailKey(email)
		if _, exists := entry.names[name]; exists {
			return false
		}
		entry.names[name] = id
	}
	entry.ids[id] = struct{}{}
	return true
}

func emailKey(email string) fingerprint {
	return sha256.Sum256([]byte(strings.ToLower(email)))
}

func effectiveID(id membership.UUID) fingerprint {
	// Xray's VLESS ProcessUUID ignores bytes 6 and 7 on lookup and deletion.
	id[6], id[7] = 0, 0
	return sha256.Sum256(id[:])
}
