package proto

type PacketKind string

const (
	PacketKindHello PacketKind = "hello"

	PacketKindAuthorizationRequest  PacketKind = "authorization/request"
	PacketKindAuthorizationQuestion PacketKind = "authorization/question"
	PacketKindAuthorizationAnswer   PacketKind = "authorization/answer"
	PacketKindAuthorizationFailure  PacketKind = "authorization/failure"
	PacketKindAuthorizationSuccess  PacketKind = "authorization/success"

	PacketKindLogs PacketKind = "logs"

	PacketKindMetricsStoreRequest   PacketKind = "metrics/store"
	PacketKindMetricsStoreV2Request PacketKind = "metrics/store_v2"

	PacketKindApplicationsStoreRequest PacketKind = "applications/store"

	PacketKindNodesStoreRequest PacketKind = "nodes/store"

	PacketKindEntitiesDeltasRequest PacketKind = "entities/deltas"
	PacketKindEntitiesResyncRequest PacketKind = "entities/resync"

	PacketKindEventLastValueRequest PacketKind = "events/query/last_value"
	PacketKindEventsStoreRequest    PacketKind = "events/store"

	PacketKindStatusStoreRequest PacketKind = "status/store"

	PacketKindBye PacketKind = "bye"

	PacketKindDecision         PacketKind = "decision"
	PacketKindDecisionFeedback PacketKind = "decision/feedback"
	PacketKindDecisionPull     PacketKind = "decision/pull"

	PacketKindRestart PacketKind = "restart"

	PacketKindRawStoreRequest PacketKind = "raw/store"
)

const (
	PacketKindPing PacketKind = "ping"
	PacketKindPong PacketKind = "pong"
)

func (kind PacketKind) String() string {
	return string(kind)
}
