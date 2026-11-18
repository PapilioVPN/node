package domain

// Counters retains the SDK's standard link counters as exact integers.
// Byte counters use bytes; other counters count packets/events. Values may wrap
// or reset when a device/driver is replaced; this is not a rate observation.
type Counters struct {
	ReceivePackets          uint64
	TransmitPackets         uint64
	ReceiveBytes            uint64
	TransmitBytes           uint64
	ReceiveErrors           uint64
	TransmitErrors          uint64
	ReceiveDropped          uint64
	TransmitDropped         uint64
	Multicast               uint64
	Collisions              uint64
	ReceiveLengthErrors     uint64
	ReceiveOverErrors       uint64
	ReceiveCRCErrors        uint64
	ReceiveFrameErrors      uint64
	ReceiveFIFOErrors       uint64
	ReceiveMissedErrors     uint64
	TransmitAbortedErrors   uint64
	TransmitCarrierErrors   uint64
	TransmitFIFOErrors      uint64
	TransmitHeartbeatErrors uint64
	TransmitWindowErrors    uint64
	ReceiveCompressed       uint64
	TransmitCompressed      uint64
}
