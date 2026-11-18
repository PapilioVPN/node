package natsapi_test

import (
	"context"

	"github.com/PapilioVPN/node/internal/apps/system/network/interfaces/domain"
)

type readerFixture struct {
	cause               error
	wait                bool
	calls, counterCalls int
}

func (reader *readerFixture) List(ctx context.Context) (domain.Snapshot, error) {
	reader.calls++
	if reader.wait {
		<-ctx.Done()
		return domain.Snapshot{}, ctx.Err()
	}
	return domain.Snapshot{Interfaces: []domain.Interface{{Index: 42, ParentNamespaceID: new(int32(0)), Name: "fixture", Kind: "dummy", MTU: 1500, Flags: 0x80000001, Addresses: []domain.Address{{InterfaceIndex: 42, Family: "ipv4", LocalPrefix: "192.0.2.4/32", PeerPrefix: new("198.51.100.5/24"), Flags: 0x80000000, Scope: 253, PreferredLifetimeSeconds: 0, ValidLifetimeSeconds: 1<<32 - 1}}}}, UnmatchedAddresses: []domain.Address{{InterfaceIndex: 77, Family: "ipv6", LocalPrefix: "fe80::4/64"}}}, reader.cause
}
func (reader *readerFixture) Counters(ctx context.Context) ([]domain.InterfaceCounters, error) {
	reader.counterCalls++
	if reader.wait {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return []domain.InterfaceCounters{{Index: 42, Name: "fixture", Counters: &domain.Counters{ReceiveBytes: 9007199254740993, TransmitBytes: 1<<64 - 1, ReceivePackets: 7, TransmitCarrierErrors: 11}}, {Index: 77, Name: "missing"}}, reader.cause
}
