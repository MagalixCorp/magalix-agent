package agent

import (
	"context"
	"os"
	"time"

	"github.com/MagalixTechnologies/uuid-go"
	"golang.org/x/sync/errgroup"
)

const AuthorizationTimeoutDuration = 2 * time.Hour

type LogLevel struct {
	Level string
}

type Agent struct {
	AccountID uuid.UUID
	ClusterID uuid.UUID
	AgentID   uuid.UUID

	EntitiesSource EntitiesSource
	Gateway        Gateway
	Auditor        Auditor

	changeLogLevel ChangeLogLevelHandler

	cancelAll     context.CancelFunc
	cancelSources context.CancelFunc
	cancelSinks   context.CancelFunc
}

func New(entitiesSource EntitiesSource, gateway Gateway, logLevelHandler ChangeLogLevelHandler, auditor Auditor) *Agent {
	return &Agent{
		EntitiesSource: entitiesSource,
		Gateway:        gateway,
		changeLogLevel: logLevelHandler,
		Auditor:        auditor,
	}
}

func (a *Agent) Start() error {
	allCtx, cancelAll := context.WithCancel(context.Background())
	a.cancelAll = cancelAll
	defer a.cancelAll()

	sourcesCtx, cancelSources := context.WithCancel(allCtx)
	a.cancelSources = cancelSources
	sinksCtx, cancelSinks := context.WithCancel(allCtx)
	a.cancelSinks = cancelSinks

	a.Auditor.SetAuditResultHandler(a.handleAuditResult)

	// Initialize and authenticate gateway
	a.Gateway.SetAuditCommandHandler(a.Auditor.HandleAuditCommand)
	a.Gateway.SetConstraintsHandler(a.Auditor.HandleConstraints)
	a.Gateway.SetRestartHandler(a.handleRestart)
	a.Gateway.SetChangeLogLevelHandler(a.handleLogLevelChange)

	eg, _ := errgroup.WithContext(allCtx)

	// Add a context to Gateway to manage the numerous go routines in the client
	eg.Go(func() error { return a.Gateway.Start(sinksCtx) })

	// Blocks until authorized. Uses a long timeout to slowdown agents that are no longer authorized.
	err := a.Gateway.WaitAuthorization(AuthorizationTimeoutDuration)
	if err != nil {
		return err
	}

	eg.Go(func() error { return a.EntitiesSource.Start(sourcesCtx) })
	eg.Go(func() error { return a.Auditor.Start(sourcesCtx) })

	return eg.Wait()
}

func (a *Agent) stopSources() error {
	if a.cancelSources == nil {
		return nil
	}
	a.cancelSources()
	a.cancelSources = nil
	return nil
}

func (a *Agent) stopSinks() error {
	if a.cancelSinks == nil {
		return nil
	}
	a.cancelSinks()
	a.cancelSinks = nil
	return nil
}

func (a *Agent) Stop() error {
	if a.cancelAll == nil {
		return nil
	}
	a.cancelAll()
	a.cancelAll = nil
	// TODO There's no way to know if workers exited with an error
	return nil
}

func (a *Agent) Exit(exitCode int) {
	os.Exit(exitCode)
}
