package proto

type PacketKind string

const (
	PacketKindHello PacketKind = "hello"

	PacketKindAuthorizationRequest PacketKind = "authorization/request"
	PacketKindAuthorizationAnswer  PacketKind = "authorization/answer"

	PacketKindLogs PacketKind = "logs"

	PacketKindMetricsStoreV2Request PacketKind = "metrics/store_v2"

	PacketKindEntitiesDeltasRequest PacketKind = "entities/deltas"
	PacketKindEntitiesResyncRequest PacketKind = "entities/resync"

	PacketKindBye PacketKind = "bye"

	PacketKindAutomation         PacketKind = "automation"
	PacketKindAutomationFeedback PacketKind = "automation/feedback"

	PacketKindRestart PacketKind = "restart"

	PacketKindRawStoreRequest PacketKind = "raw/store"

	PacketKindLogLevel           PacketKind = "loglevel"
	PacketKindConstraintsRequest PacketKind = "audit/constraints"
	PacketKindAuditResultRequest PacketKind = "audit/result"
)

const (
	PacketKindPing PacketKind = "ping"
)

func (kind PacketKind) String() string {
	return string(kind)
}
