package agent

import (
	"math/rand"
	"time"

	"github.com/MagalixTechnologies/core/logger"
)

func (a *Agent) handleDeltas(deltas []*Delta) error {
	return a.Gateway.SendEntitiesDeltas(deltas)
}

func (a *Agent) handleResync(resync *EntitiesResync) error {
	return a.Gateway.SendEntitiesResync(resync)
}

func (a *Agent) handleAuditResult(auditResult []*AuditResult) error {
	if len(auditResult) == 0 {
		return nil
	}
	return a.Gateway.SendAuditResults(auditResult)
}

func (a *Agent) handleRestart() error {
	go func() {
		logger.Info("Received restart. Stopping workers.")
		if err := a.stopSources(); err != nil {
			logger.Errorf("failed to stop agent sources. %s", err)
		}

		if err := logger.Sync(); err != nil {
			logger.Errorf("failed to sync logs. %s", err)
		}

		if err := a.stopSinks(); err != nil {
			logger.Errorf("failed to stop agent sinks. %s", err)
		}

		// Set a random wait time of up to 600 seconds (10 minutes)
		waitTime := time.Duration(rand.Intn(600)) * time.Second
		logger.Infof("Agent will exit in %s at %s", waitTime.String(), time.Now().Add(waitTime))
		time.Sleep(waitTime)

		a.Exit(0)
	}()
	return nil
}

func (a *Agent) handleLogLevelChange(level *LogLevel) error {
	return a.changeLogLevel(level)
}
