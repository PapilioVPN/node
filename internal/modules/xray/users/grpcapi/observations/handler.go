// Package observations adapts user membership counts and records to gRPC.
package observations

// ObservationAPI retains independent count, metadata and credential-read grants.
type ObservationAPI struct {
	UserCounts            InboundUserCountReader
	Users                 InboundUserReader
	AllowUsersRead        bool
	AllowUserMetadataRead bool
	AllowUserAccountsRead bool
}
