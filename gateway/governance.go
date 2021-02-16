package gateway

import (
	"math"
	"time"

	"github.com/MagalixCorp/magalix-agent/v2/agent"
	"github.com/MagalixCorp/magalix-agent/v2/proto"
	"github.com/MagalixTechnologies/core/logger"
)

const (
	auditResultPacketExpireAfter = 30 * time.Minute
	auditResultPacketExpireCount = 0
	auditResultPacketPriority    = 1
	auditResultPacketRetries     = 5

	auditResultsBatchMaxSize = 1000
)

func (g *MagalixGateway) SetConstraintHandler(handler agent.ConstraintHandler) {
	if handler == nil {
		panic("constraints handler is nil")
	}
	g.addConstraint = handler
	g.gwClient.AddListener(proto.PacketKindConstraintsRequest, func(in []byte) ([]byte, error) {
		var constraintsRequest proto.PacketConstraintsRequest
		if err := proto.DecodeSnappy(in, &constraintsRequest); err != nil {
			return nil, err
		}

		for _, c := range constraintsRequest.Constraints {
			constraint := &agent.Constraint{
				Id:           c.Id,
				TemplateId:   c.TemplateId,
				AccountId:    c.AccountId,
				ClusterId:    c.ClusterId,
				Name:         c.Name,
				TemplateName: c.TemplateName,
				Parameters:   c.Parameters,
				Match: agent.Match{
					Namespaces: c.Match.Namespaces,
					Kinds:      c.Match.Kinds,
				},
				Code:      c.Code,
				UpdatedAt: c.UpdatedAt,
			}
			err := g.addConstraint(constraint)
			if err != nil {
				logger.Errorw("Couldn't add constraint", "error", err, "constraint-id", c.Id)
			}
		}

		return proto.EncodeSnappy(proto.PacketConstraintsResponse{})
	})
}

func (g *MagalixGateway) SendAuditResults(auditResults []*agent.AuditResult) error {
	noOfBatches := int(math.Ceil(float64(len(auditResults)) / float64(auditResultsBatchMaxSize)))
	lastBatchSize := len(auditResults) % auditResultsBatchMaxSize
	for i := 0; i < noOfBatches; i++ {
		start := i * auditResultsBatchMaxSize
		var end int
		if i == noOfBatches-1 && lastBatchSize > 0 {
			end = start + lastBatchSize
		} else {
			end = start + auditResultsBatchMaxSize
		}
		g.SendAuditResultsBatch(auditResults[start:end])
	}
	return nil
}

func (g *MagalixGateway) SendAuditResultsBatch(auditResult []*agent.AuditResult) {
	items := make([]*proto.PacketAuditResultItem, 0, len(auditResult))
	for _, r := range auditResult {
		item := proto.PacketAuditResultItem{
			TemplateID:    r.TemplateID,
			ConstraintID:  r.ConstraintID,
			HasViolation:  r.HasViolation,
			Msg:           r.Msg,
			EntityName:    r.EntityName,
			EntityKind:    r.EntityKind,
			NamespaceName: r.NamespaceName,
			ParentName:    r.ParentName,
			ParentKind:    r.ParentKind,
			NodeIP:        r.NodeIP,
		}

		items = append(items, &item)

	}
	logger.Infof("Sending  %d audit results", len(auditResult))
	packet := proto.PacketAuditResultRequest{
		Items:     items,
		Timestamp: time.Now().UTC(),
	}
	//g.gwClient.Pipe(client.Package{
	//	Kind:        proto.PacketKindAuditResultRequest,
	//	ExpiryTime:  utils.After(auditResultPacketExpireAfter),
	//	ExpiryCount: auditResultPacketExpireCount,
	//	Priority:    auditResultPacketPriority,
	//	Retries:     auditResultPacketRetries,
	//	Data:        packet,
	//})

	logger.Info("===RECEIVED AUDIT RESULT", packet)
}
