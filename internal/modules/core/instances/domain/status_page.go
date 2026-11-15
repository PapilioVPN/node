package domain

// StatusPageInput selects IDs strictly after AfterID in lexicographic order.
// PageSize must be positive; the current configured count bounds the result.
type StatusPageInput struct {
	AfterID  InstanceID
	PageSize int
}

// StatusPage contains independent observations, not an atomic runtime snapshot.
// An empty NextAfterID means no configured IDs remain after this page.
type StatusPage struct {
	Items       []Status
	NextAfterID InstanceID
}
