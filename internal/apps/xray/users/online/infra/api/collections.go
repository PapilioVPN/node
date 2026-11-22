package api

import (
	"context"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/xray/users/online/domain"
	stats "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
)

// Names uses the native full-key projection, preserving opaque embedded >>>.
// Each first observation makes one RPC; continuations are owned by the service.
func (r Reader) Names(ctx context.Context) ([]domain.Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	reply, err := r.client.GetAllOnlineUsers(ctx, &stats.GetAllOnlineUsersRequest{}, grpc.MaxCallRecvMsgSize(domain.MaxNativeBytes))
	if err != nil {
		return nil, failure(ctx, err)
	}
	if reply == nil {
		return nil, domain.ErrUnavailable
	}
	if len(reply.Users) > domain.MaxRecords {
		return nil, domain.ErrCapacity
	}
	rows := make([]domain.Record, len(reply.Users))
	for i, key := range reply.Users {
		if len(key) <= len("user>>>")+len(">>>online") || !strings.HasPrefix(key, "user>>>") || !strings.HasSuffix(key, ">>>online") {
			return nil, domain.ErrUnavailable
		}
		email := strings.TrimSuffix(strings.TrimPrefix(key, "user>>>"), ">>>online")
		if email == "" || !utf8.ValidString(email) {
			return nil, domain.ErrUnavailable
		}
		rows[i].Email = email
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Email < rows[j].Email })
	return rows, nil
}

// Records uses native bulk IPs, with zero per-user RPCs and no traffic scan/reset.
// The pinned GetUsersStats splits emails on >>> and merges colliding prefixes.
// A full-key bulk guard rejects that known lossy source. The guard and detail
// scan are non-atomic; newly appearing unguarded identities reject the result.
// Native clients can still change membership between observations; no engine
// transaction or endpoint attestation is implied by the local generation.
func (r Reader) Records(ctx context.Context) ([]domain.Record, error) {
	names, err := r.Names(ctx)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]bool, len(names))
	for _, row := range names {
		if strings.Contains(row.Email, ">>>") {
			return nil, domain.ErrIdentityUnsupported
		}
		allowed[row.Email] = true
	}
	reply, err := r.client.GetUsersStats(ctx, &stats.GetUsersStatsRequest{IncludeTraffic: false, Reset_: false}, grpc.MaxCallRecvMsgSize(domain.MaxNativeBytes))
	if err != nil {
		return nil, failure(ctx, err)
	}
	if reply == nil {
		return nil, domain.ErrUnavailable
	}
	if len(reply.Users) > domain.MaxRecords {
		return nil, domain.ErrCapacity
	}
	rows := make([]domain.Record, 0)
	seen := make(map[string]bool, len(reply.Users))
	for _, user := range reply.Users {
		if user == nil || user.Email == "" || !utf8.ValidString(user.Email) || seen[user.Email] || user.Traffic != nil {
			return nil, domain.ErrUnavailable
		}
		if !allowed[user.Email] {
			return nil, domain.ErrUnavailable
		}
		seen[user.Email] = true
		if len(user.Ips) > domain.MaxRecords-len(rows) {
			return nil, domain.ErrCapacity
		}
		seenIP := make(map[string]bool, len(user.Ips))
		for _, ip := range user.Ips {
			if ip == nil || ip.Ip == "" || !utf8.ValidString(ip.Ip) || seenIP[ip.Ip] {
				return nil, domain.ErrUnavailable
			}
			seenIP[ip.Ip] = true
			rows = append(rows, domain.Record{Email: user.Email, IP: &domain.IP{Address: ip.Ip, LastSeenUnixSeconds: ip.LastSeen}})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Email != rows[j].Email {
			return rows[i].Email < rows[j].Email
		}
		return rows[i].IP.Address < rows[j].IP.Address
	})
	return rows, nil
}
