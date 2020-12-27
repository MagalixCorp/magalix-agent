package agent

import (
	"context"
	"github.com/MagalixTechnologies/uuid-go"
	"golang.org/x/sync/errgroup"
	"os"
)

type LogLevel struct {
	Level string
}

type Agent struct {
	AccountID uuid.UUID
	ClusterID uuid.UUID
	AgentID   uuid.UUID

	MetricsSource      MetricsSource
	EntitiesSource     EntitiesSource
	AutomationExecutor AutomationExecutor
	Gateway            Gateway

	changeLogLevel ChangeLogLevelHandler

	cancelAll     context.CancelFunc
	cancelSources context.CancelFunc
	cancelSinks   context.CancelFunc
}

func New(
	metricsSource MetricsSource,
	entitiesSource EntitiesSource,
	automationExecutor AutomationExecutor,
	gateway Gateway,
	logLevelHandler ChangeLogLevelHandler,
) *Agent {
	return &Agent{
		MetricsSource:      metricsSource,
		EntitiesSource:     entitiesSource,
		AutomationExecutor: automationExecutor,
		Gateway:            gateway,
		changeLogLevel:     logLevelHandler,
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

	// Initialize and authenticate gateway
	a.Gateway.SetAutomationHandler(a.AutomationExecutor.SubmitAutomation)
	a.Gateway.SetRestartHandler(a.handleRestart)
	a.Gateway.SetChangeLogLevelHandler(a.handleLogLevelChange)

	a.AutomationExecutor.SetAutomationFeedbackHandler(a.handleAutomationFeedback)

	a.EntitiesSource.SetDeltasHandler(a.handleDeltas)
	a.EntitiesSource.SetEntitiesResyncHandler(a.handleResync)

	a.MetricsSource.SetMetricsHandler(a.handleMetrics)

	eg, _ := errgroup.WithContext(allCtx)
	// Add a context to Gateway to manage the numerous go routines in the client
	eg.Go(func() error { return a.Gateway.Start(sinksCtx) })
	// Blocks until authorized
	a.Gateway.WaitAuthorization()

	eg.Go(func() error { return a.EntitiesSource.Start(sourcesCtx) })
	eg.Go(func() error { return a.MetricsSource.Start(sourcesCtx) })
	eg.Go(func() error { return a.AutomationExecutor.Start(sourcesCtx) })

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
