// Package api owns exact native StatsService online reads on a borrowed channel.
package api

import (
	"context"
	"errors"
	"sort"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/xray/users/online/domain"
	stats "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Reader struct{ client stats.StatsServiceClient }

func New(connection grpc.ClientConnInterface) Reader {
	return Reader{stats.NewStatsServiceClient(connection)}
}

func (r Reader) Count(ctx context.Context, email string) (domain.Summary, error) {
	if err := ctx.Err(); err != nil {
		return domain.Summary{}, err
	}
	name := "user>>>" + email + ">>>online"
	reply, err := r.client.GetStatsOnline(ctx, &stats.GetStatsRequest{Name: name})
	if status.Code(err) == codes.NotFound {
		return domain.Summary{}, nil
	}
	if err != nil {
		return domain.Summary{}, failure(ctx, err)
	}
	if reply == nil || reply.Stat == nil || reply.Stat.Name != name || reply.Stat.Value < 0 {
		return domain.Summary{}, domain.ErrUnavailable
	}
	return domain.Summary{Count: &reply.Stat.Value}, nil
}

// IPs materializes one bounded targeted native reply for the paging owner.
// Receive bytes do not bound native scans, allocation or protobuf heap usage.
func (r Reader) IPs(ctx context.Context, email string) (domain.Visitors, error) {
	if err := ctx.Err(); err != nil {
		return domain.Visitors{}, err
	}
	name := "user>>>" + email + ">>>online"
	reply, err := r.client.GetStatsOnlineIpList(ctx, &stats.GetStatsRequest{Name: name}, grpc.MaxCallRecvMsgSize(domain.MaxNativeBytes))
	if status.Code(err) == codes.NotFound {
		return domain.Visitors{}, nil
	}
	if err != nil {
		return domain.Visitors{}, failure(ctx, err)
	}
	if reply == nil || reply.Name != name {
		return domain.Visitors{}, domain.ErrUnavailable
	}
	if len(reply.Ips) > domain.MaxRecords {
		return domain.Visitors{}, domain.ErrCapacity
	}
	rows := make([]domain.IP, 0, len(reply.Ips))
	for ip, seen := range reply.Ips {
		if ip == "" || !utf8.ValidString(ip) {
			return domain.Visitors{}, domain.ErrUnavailable
		}
		rows = append(rows, domain.IP{Address: ip, LastSeenUnixSeconds: seen})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Address < rows[j].Address })
	return domain.Visitors{Tracked: true, IPs: rows}, nil
}

func failure(ctx context.Context, err error) error {
	marker := domain.ErrUnavailable
	switch {
	case status.Code(err) == codes.Unimplemented:
		marker = domain.ErrUnsupported
	case status.Code(err) == codes.ResourceExhausted:
		marker = domain.ErrCapacity
	case status.Code(err) == codes.Canceled:
		marker = context.Canceled
	case status.Code(err) == codes.DeadlineExceeded:
		marker = context.DeadlineExceeded
	}
	return errors.Join(marker, err, ctx.Err())
}
