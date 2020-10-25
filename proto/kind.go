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

	PacketKindMetricsStoreV2Request PacketKind = "metrics/store_v2"

	PacketKindEntitiesDeltasRequest PacketKind = "entities/deltas"
	PacketKindEntitiesResyncRequest PacketKind = "entities/resync"

	PacketKindBye PacketKind = "bye"

	PacketKindDecision         PacketKind = "decision"
	PacketKindDecisionFeedback PacketKind = "decision/feedback"
	PacketKindDecisionPull     PacketKind = "decision/pull"

	PacketKindRestart PacketKind = "restart"

	PacketKindRawStoreRequest PacketKind = "raw/store"

	PacketKindLogLevel PacketKind = "loglevel"
)

const (
	PacketKindPing PacketKind = "ping"
	PacketKindPong PacketKind = "pong"
)

func (kind PacketKind) String() string {
	return string(kind)
}
