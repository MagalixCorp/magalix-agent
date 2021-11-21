package proto

type PacketKind string

const (
	PacketKindHello                PacketKind = "hello"
	PacketKindAuthorizationRequest PacketKind = "authorization/request"
	PacketKindAuthorizationAnswer  PacketKind = "authorization/answer"
	PacketKindLogs                 PacketKind = "logs"
	PacketKindBye                  PacketKind = "bye"
	PacketKindRestart              PacketKind = "restart"
	PacketKindRawStoreRequest      PacketKind = "raw/store"
	PacketKindLogLevel             PacketKind = "loglevel"
	PacketKindConstraintsRequest   PacketKind = "audit/constraints"
	PacketKindAuditResultRequest   PacketKind = "audit/result"
	PacketKindAuditCommand         PacketKind = "audit/audit_command"
	PacketKindPing                 PacketKind = "ping"
)

func (kind PacketKind) String() string {
	return string(kind)
}
