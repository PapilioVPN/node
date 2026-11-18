package httpapi

import (
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/network/interfaces/domain"
)

type addressResponse struct {
	InterfaceIndex           uint32  `json:"interface_index"`
	Family                   string  `json:"family"`
	LocalPrefix              string  `json:"local_prefix"`
	PeerPrefix               *string `json:"peer_prefix"`
	Broadcast                *string `json:"broadcast"`
	Label                    string  `json:"label"`
	Flags                    uint32  `json:"flags"`
	Scope                    uint32  `json:"scope"`
	PreferredLifetimeSeconds uint32  `json:"preferred_lifetime_seconds"`
	ValidLifetimeSeconds     uint32  `json:"valid_lifetime_seconds"`
}
type interfaceResponse struct {
	ParentNamespaceID *int32            `json:"parent_namespace_id"`
	Index             uint32            `json:"index"`
	Name              string            `json:"name"`
	Kind              string            `json:"kind"`
	MTU               uint32            `json:"mtu"`
	HardwareAddress   string            `json:"hardware_address"`
	Flags             uint32            `json:"flags"`
	OperationalState  uint32            `json:"operational_state"`
	ParentIndex       uint32            `json:"parent_index"`
	MasterIndex       uint32            `json:"master_index"`
	Addresses         []addressResponse `json:"addresses"`
}
type listResponse struct {
	Interfaces         []interfaceResponse `json:"interfaces"`
	UnmatchedAddresses []addressResponse   `json:"unmatched_addresses"`
	ObservedAt         time.Time           `json:"observed_at"`
	DurationNS         int64               `json:"duration_ns,string"`
}
type countersResponse struct {
	ReceivePackets          uint64 `json:"receive_packets,string"`
	TransmitPackets         uint64 `json:"transmit_packets,string"`
	ReceiveBytes            uint64 `json:"receive_bytes,string"`
	TransmitBytes           uint64 `json:"transmit_bytes,string"`
	ReceiveErrors           uint64 `json:"receive_errors,string"`
	TransmitErrors          uint64 `json:"transmit_errors,string"`
	ReceiveDropped          uint64 `json:"receive_dropped,string"`
	TransmitDropped         uint64 `json:"transmit_dropped,string"`
	Multicast               uint64 `json:"multicast,string"`
	Collisions              uint64 `json:"collisions,string"`
	ReceiveLengthErrors     uint64 `json:"receive_length_errors,string"`
	ReceiveOverErrors       uint64 `json:"receive_over_errors,string"`
	ReceiveCRCErrors        uint64 `json:"receive_crc_errors,string"`
	ReceiveFrameErrors      uint64 `json:"receive_frame_errors,string"`
	ReceiveFIFOErrors       uint64 `json:"receive_fifo_errors,string"`
	ReceiveMissedErrors     uint64 `json:"receive_missed_errors,string"`
	TransmitAbortedErrors   uint64 `json:"transmit_aborted_errors,string"`
	TransmitCarrierErrors   uint64 `json:"transmit_carrier_errors,string"`
	TransmitFIFOErrors      uint64 `json:"transmit_fifo_errors,string"`
	TransmitHeartbeatErrors uint64 `json:"transmit_heartbeat_errors,string"`
	TransmitWindowErrors    uint64 `json:"transmit_window_errors,string"`
	ReceiveCompressed       uint64 `json:"receive_compressed,string"`
	TransmitCompressed      uint64 `json:"transmit_compressed,string"`
}
type interfaceCountersResponse struct {
	Index    uint32            `json:"index"`
	Name     string            `json:"name"`
	Counters *countersResponse `json:"counters"`
}
type counterObservationResponse struct {
	Interfaces []interfaceCountersResponse `json:"interfaces"`
	ObservedAt time.Time                   `json:"observed_at"`
	DurationNS int64                       `json:"duration_ns,string"`
}

func addressesResponse(rows []domain.Address) []addressResponse {
	result := make([]addressResponse, 0, len(rows))
	for _, row := range rows {
		result = append(result, addressResponse{
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

func listResult(result domain.Observation) listResponse {
	rows := make([]interfaceResponse, 0, len(result.Snapshot.Interfaces))
	for _, row := range result.Snapshot.Interfaces {
		rows = append(rows, interfaceResponse{
			Index:             row.Index,
			Name:              row.Name,
			Kind:              row.Kind,
			MTU:               row.MTU,
			HardwareAddress:   row.HardwareAddress,
			Flags:             row.Flags,
			OperationalState:  row.OperationalState,
			ParentNamespaceID: row.ParentNamespaceID, ParentIndex: row.ParentIndex,
			MasterIndex: row.MasterIndex,
			Addresses:   addressesResponse(row.Addresses),
		})
	}
	return listResponse{Interfaces: rows, UnmatchedAddresses: addressesResponse(result.Snapshot.UnmatchedAddresses), ObservedAt: result.ObservedAt, DurationNS: int64(result.Duration)}
}

func counterResult(result domain.CounterObservation) counterObservationResponse {
	rows := make([]interfaceCountersResponse, 0, len(result.Interfaces))
	for _, row := range result.Interfaces {
		var counters *countersResponse
		if row.Counters != nil {
			counters = &countersResponse{
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
				ReceiveCRCErrors:        row.Counters.ReceiveCRCErrors,
				ReceiveFrameErrors:      row.Counters.ReceiveFrameErrors,
				ReceiveFIFOErrors:       row.Counters.ReceiveFIFOErrors,
				ReceiveMissedErrors:     row.Counters.ReceiveMissedErrors,
				TransmitAbortedErrors:   row.Counters.TransmitAbortedErrors,
				TransmitCarrierErrors:   row.Counters.TransmitCarrierErrors,
				TransmitFIFOErrors:      row.Counters.TransmitFIFOErrors,
				TransmitHeartbeatErrors: row.Counters.TransmitHeartbeatErrors,
				TransmitWindowErrors:    row.Counters.TransmitWindowErrors,
				ReceiveCompressed:       row.Counters.ReceiveCompressed,
				TransmitCompressed:      row.Counters.TransmitCompressed,
			}
		}
		rows = append(rows, interfaceCountersResponse{Index: row.Index, Name: row.Name, Counters: counters})
	}
	return counterObservationResponse{Interfaces: rows, ObservedAt: result.ObservedAt, DurationNS: int64(result.Duration)}
}
