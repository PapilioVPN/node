// Package grpcapi adapts network interface observations to versioned gRPC.
package grpcapi

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/network/interfaces/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/network/interfaces/domain"
	interfacesv1 "github.com/PapilioVPN/node/internal/apps/system/network/interfaces/grpcapi/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Handler struct {
	interfacesv1.UnimplementedInterfacesServiceServer
	Service services.Service
	Timeout time.Duration
}

func (handler Handler) Register(server grpc.ServiceRegistrar) {
	interfacesv1.RegisterInterfacesServiceServer(server, handler)
}
func (handler Handler) ListInterfaces(ctx context.Context, _ *interfacesv1.ListInterfacesRequest) (*interfacesv1.ListInterfacesResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, handler.Timeout)
	defer cancel()
	result, err := handler.Service.List(ctx)
	if err != nil {
		return nil, readError(err)
	}
	rows := make([]*interfacesv1.Interface, 0, len(result.Snapshot.Interfaces))
	for _, row := range result.Snapshot.Interfaces {
		rows = append(rows, &interfacesv1.Interface{
			Index:             row.Index,
			Name:              row.Name,
			Kind:              row.Kind,
			Mtu:               row.MTU,
			HardwareAddress:   row.HardwareAddress,
			Flags:             row.Flags,
			OperationalState:  row.OperationalState,
			ParentNamespaceId: row.ParentNamespaceID, ParentIndex: row.ParentIndex,
			MasterIndex: row.MasterIndex,
			Addresses:   addressResults(row.Addresses),
		})
	}
	return &interfacesv1.ListInterfacesResponse{Interfaces: rows, UnmatchedAddresses: addressResults(result.Snapshot.UnmatchedAddresses), ObservedAt: timestamppb.New(result.ObservedAt), DurationNs: int64(result.Duration)}, nil
}
func addressResults(rows []domain.Address) []*interfacesv1.Address {
	result := make([]*interfacesv1.Address, 0, len(rows))
	for _, row := range rows {
		result = append(result, &interfacesv1.Address{
			InterfaceIndex:           row.InterfaceIndex,
			Family:                   row.Family,
			LocalPrefix:              row.LocalPrefix,
			PeerPrefix:               row.PeerPrefix,
			Broadcast:                row.Broadcast,
			Label:                    row.Label,
			Flags:                    row.Flags,
			Scope:                    row.Scope,
			PreferredLifetimeSeconds: row.PreferredLifetimeSeconds,
			ValidLifetimeSeconds:     row.ValidLifetimeSeconds,
		})
	}
	return result
}
func (handler Handler) GetInterfaceCounters(ctx context.Context, _ *interfacesv1.GetInterfaceCountersRequest) (*interfacesv1.GetInterfaceCountersResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, handler.Timeout)
	defer cancel()
	result, err := handler.Service.ReadCounters(ctx)
	if err != nil {
		return nil, readError(err)
	}
	rows := make([]*interfacesv1.InterfaceCounters, 0, len(result.Interfaces))
	for _, row := range result.Interfaces {
		var counters *interfacesv1.Counters
		if row.Counters != nil {
			counters = &interfacesv1.Counters{
				ReceivePackets:          row.Counters.ReceivePackets,
				TransmitPackets:         row.Counters.TransmitPackets,
				ReceiveBytes:            row.Counters.ReceiveBytes,
				TransmitBytes:           row.Counters.TransmitBytes,
				ReceiveErrors:           row.Counters.ReceiveErrors,
				TransmitErrors:          row.Counters.TransmitErrors,
				ReceiveDropped:          row.Counters.ReceiveDropped,
				TransmitDropped:         row.Counters.TransmitDropped,
				Multicast:               row.Counters.Multicast,
				Collisions:              row.Counters.Collisions,
				ReceiveLengthErrors:     row.Counters.ReceiveLengthErrors,
				ReceiveOverErrors:       row.Counters.ReceiveOverErrors,
				ReceiveCrcErrors:        row.Counters.ReceiveCRCErrors,
				ReceiveFrameErrors:      row.Counters.ReceiveFrameErrors,
				ReceiveFifoErrors:       row.Counters.ReceiveFIFOErrors,
				ReceiveMissedErrors:     row.Counters.ReceiveMissedErrors,
				TransmitAbortedErrors:   row.Counters.TransmitAbortedErrors,
				TransmitCarrierErrors:   row.Counters.TransmitCarrierErrors,
				TransmitFifoErrors:      row.Counters.TransmitFIFOErrors,
				TransmitHeartbeatErrors: row.Counters.TransmitHeartbeatErrors,
				TransmitWindowErrors:    row.Counters.TransmitWindowErrors,
				ReceiveCompressed:       row.Counters.ReceiveCompressed,
				TransmitCompressed:      row.Counters.TransmitCompressed,
			}
		}
		rows = append(rows, &interfacesv1.InterfaceCounters{Index: row.Index, Name: row.Name, Counters: counters})
	}
	return &interfacesv1.GetInterfaceCountersResponse{Interfaces: rows, ObservedAt: timestamppb.New(result.ObservedAt), DurationNs: int64(result.Duration)}, nil
}
func readError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return status.FromContextError(err).Err()
	}
	return status.Error(codes.Unavailable, "network interfaces unavailable")
}
