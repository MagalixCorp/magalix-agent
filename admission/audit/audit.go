package audit

import (
	"context"
	"fmt"
	"time"

	"github.com/MagalixTechnologies/core/logger"
	opa "github.com/open-policy-agent/frameworks/constraint/pkg/client"
	"github.com/open-policy-agent/frameworks/constraint/pkg/types"
	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const auditInterval = time.Minute

type AuditHandler struct {
	opa *opa.Client
}

func NewAuditHandler(opaClient *opa.Client) AuditHandler {
	return AuditHandler{opa: opaClient}
}

func (ah *AuditHandler) audit(ctx context.Context) ([]*types.Result, error) {
	resp, err := ah.opa.Audit(ctx)
	if err != nil {
		logger.Errorw("Error while performing audit", "error", err)
		return nil, err
	}
	for _, r := range resp.Results() {
		obj := r.Resource.(*unstructured.Unstructured)
		logger.Infof("Found audit violation: %s for %s", r.Msg, obj.GetName())
		fmt.Println("Found violation for", r.Msg, obj.GetName())
	}
	return resp.Results(), nil
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
