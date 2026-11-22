package grpcapi

import (
	"context"

	"github.com/PapilioVPN/node/internal/apps/xray/users/online/domain"
	onlinev1 "github.com/PapilioVPN/node/internal/apps/xray/users/online/grpcapi/proto/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (h Handler) ListOnlineUsers(ctx context.Context, r *onlinev1.ListOnlineUsersRequest) (*onlinev1.OnlinePage, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	return h.page(ctx, domain.PageRequest{Selection: domain.Selection{InstanceID: r.InstanceId}, PageSize: int(r.PageSize), PageToken: r.PageToken}, h.Service.List)
}
func (h Handler) ListOnlineRecords(ctx context.Context, r *onlinev1.ListOnlineRecordsRequest) (*onlinev1.OnlinePage, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	return h.page(ctx, domain.PageRequest{Selection: domain.Selection{InstanceID: r.InstanceId}, PageSize: int(r.PageSize), PageToken: r.PageToken}, h.Service.Records)
}
func (h Handler) page(ctx context.Context, r domain.PageRequest, operation func(context.Context, domain.PageRequest) (domain.Page, error)) (*onlinev1.OnlinePage, error) {
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	p, err := operation(ctx, r)
	if err != nil {
		return nil, failure(err)
	}
	rows := make([]*onlinev1.OnlineRecord, len(p.Records))
	for i, row := range p.Records {
		rows[i] = &onlinev1.OnlineRecord{Email: row.Email}
		if row.IP != nil {
			rows[i].Ip = &onlinev1.OnlineIP{Ip: row.IP.Address, LastSeenUnixSeconds: row.IP.LastSeenUnixSeconds}
		}
	}
	return &onlinev1.OnlinePage{Observation: pageObservation(p), Page: paging(p), Records: rows}, nil
}
func pageObservation(p domain.Page) *onlinev1.OnlineObservation {
	o := metadata(p.Observation)
	o.Source = p.Source
	return o
}
func paging(p domain.Page) *onlinev1.OnlinePaging {
	return &onlinev1.OnlinePaging{ObservationId: p.ObservationID, ExpiresAt: timestamppb.New(p.ExpiresAt), ServedAt: timestamppb.New(p.ServedAt), Fresh: p.Fresh, Total: uint32(p.Total), NextPageToken: p.NextPageToken}
}
