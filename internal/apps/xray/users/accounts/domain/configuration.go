package domain

// InboundUserSet replaces the complete native account collection of one exact
// inbound. Empty Accounts clears it; other inbounds and settings are retained.
// Accounts are native protocol.User protobuf, not product-user records.
type InboundUserSet struct {
	InboundTag string
	Accounts   [][]byte
}
