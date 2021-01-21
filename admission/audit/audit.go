package audit

import (
	"context"
	"time"

	"github.com/MagalixCorp/magalix-agent/v2/agent"
	"github.com/MagalixTechnologies/core/logger"
	opa "github.com/open-policy-agent/frameworks/constraint/pkg/client"
	"github.com/open-policy-agent/frameworks/constraint/pkg/types"
	"golang.org/x/sync/errgroup"
)

const auditInterval = 24 * time.Hour

type AuditHandler struct {
	opa      *opa.Client
	sendRecs agent.RecsHandler
}

func NewAuditHandler(opaClient *opa.Client) *AuditHandler {
	return &AuditHandler{opa: opaClient}
}

func (ah *AuditHandler) SetRecommendationHandler(handler agent.RecsHandler) {
	ah.sendRecs = handler
}

func (ah *AuditHandler) audit(ctx context.Context) ([]*types.Result, error) {
	resp, err := ah.opa.Audit(ctx)
	if err != nil {
		logger.Errorw("Error while performing audit", "error", err)
		return nil, err
	}
	results := resp.Results()

	err = ah.sendRecs(results)
	if err != nil {
		logger.Errorw("Error while sending recommendations", "error", err)
		return nil, err
	}
	return results, nil
}

func (ah *AuditHandler) Start(ctx context.Context) error {
	logger.Info("Start entities Audit")
	eg, egCtx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		return ah.StartAuditTicker(egCtx)
	})
	return eg.Wait()
}

func (ah *AuditHandler) StartAuditTicker(ctx context.Context) error {
	auditTicker := time.NewTicker(auditInterval)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-auditTicker.C:
			_, err := ah.audit(ctx)
			return err
		}
	}
}

func (ah *AuditHandler) Stop() error {
	// TODO
	return nil
}
