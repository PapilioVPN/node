package netlink

import (
	"github.com/PapilioVPN/node/internal/apps/system/network/interfaces/domain"
	native "github.com/vishvananda/netlink"
)

func counterRecord(value *native.LinkStatistics) *domain.Counters {
	if value == nil {
		return nil
	}
	return &domain.Counters{
		ReceivePackets:          value.RxPackets,
		TransmitPackets:         value.TxPackets,
		ReceiveBytes:            value.RxBytes,
		TransmitBytes:           value.TxBytes,
		ReceiveErrors:           value.RxErrors,
		TransmitErrors:          value.TxErrors,
		ReceiveDropped:          value.RxDropped,
		TransmitDropped:         value.TxDropped,
		Multicast:               value.Multicast,
		Collisions:              value.Collisions,
		ReceiveLengthErrors:     value.RxLengthErrors,
		ReceiveOverErrors:       value.RxOverErrors,
		ReceiveCRCErrors:        value.RxCrcErrors,
		ReceiveFrameErrors:      value.RxFrameErrors,
		ReceiveFIFOErrors:       value.RxFifoErrors,
		ReceiveMissedErrors:     value.RxMissedErrors,
		TransmitAbortedErrors:   value.TxAbortedErrors,
		TransmitCarrierErrors:   value.TxCarrierErrors,
		TransmitFIFOErrors:      value.TxFifoErrors,
		TransmitHeartbeatErrors: value.TxHeartbeatErrors,
		TransmitWindowErrors:    value.TxWindowErrors,
		ReceiveCompressed:       value.RxCompressed,
		TransmitCompressed:      value.TxCompressed,
	}
}
