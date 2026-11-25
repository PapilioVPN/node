package lifecycle

import "slices"

type LiveField uint8

const (
	LiveConflict LiveField = iota
	LiveMTU
	LivePort
	LiveAddresses
	LivePrivateKey
)

func (checkpoint Checkpoint) liveField(desired Config) LiveField {
	previous, next := checkpoint.Fixed, desired.fixed()
	if previous.InitialPeersSHA256 != next.InitialPeersSHA256 {
		return LiveConflict
	}
	field := LiveConflict
	count := 0
	for _, candidate := range [...]struct {
		field   LiveField
		changed bool
	}{
		{LiveMTU, checkpoint.MTU != desired.MTU},
		{LivePort, previous.ListenPort != next.ListenPort},
		{LiveAddresses, !slices.Equal(previous.Addresses, next.Addresses)},
		{LivePrivateKey, previous.PrivateKeySHA256 != next.PrivateKeySHA256},
	} {
		if candidate.changed {
			field = candidate.field
			count++
		}
	}
	if count != 1 {
		return LiveConflict
	}
	return field
}
