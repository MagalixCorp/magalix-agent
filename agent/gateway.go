package agent

import (
	"context"
	"time"
)

type RestartHandler func() error
type ChangeLogLevelHandler func(level *LogLevel) error
type ConstraintsHandler func(constraints []*Constraint) map[string]error
type AuditCommandHandler func() error

type Gateway interface {
	Start(ctx context.Context) error
	WaitAuthorization(timeout time.Duration) error
	// TODO: Add Sync() function to ensure all buffered data is sent before exit

	SendEntitiesDeltas(deltas []*Delta) error
	SendEntitiesResync(resync *EntitiesResync) error
	SendAuditResults(auditResult []*AuditResult) error

	SetRestartHandler(handler RestartHandler)
	SetChangeLogLevelHandler(handler ChangeLogLevelHandler)
	SetConstraintsHandler(handler ConstraintsHandler)
	SetAuditCommandHandler(handler AuditCommandHandler)
}
