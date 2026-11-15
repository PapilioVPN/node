package domain

type NodeID string

type Capability string

const ReadNodeInfo Capability = "node.info.read"

const ReadInstanceStatus Capability = "instance.status.read"

// NodeInfo describes this binary's implemented management capabilities.
type NodeInfo struct {
	NodeID       NodeID
	Version      string
	APIVersion   string
	Capabilities []Capability
}
