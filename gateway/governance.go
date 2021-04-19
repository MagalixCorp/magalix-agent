package gateway

import (
	"math"
	"time"

	"github.com/MagalixCorp/magalix-agent/v3/agent"
	"github.com/MagalixCorp/magalix-agent/v3/client"
	"github.com/MagalixCorp/magalix-agent/v3/proto"
	"github.com/MagalixCorp/magalix-agent/v3/utils"
	"github.com/MagalixTechnologies/core/logger"
)

const (
	auditResultPacketExpireAfter = 30 * time.Minute
	auditResultPacketExpireCount = 0
	auditResultPacketPriority    = 1
	auditResultPacketRetries     = 5

	auditResultsBatchMaxSize = 1000
)

func (g *MagalixGateway) SetConstraintsHandler(handler agent.ConstraintsHandler) {
	if handler == nil {
		panic("constraints handler is nil")
	}
	g.addConstraints = handler
	g.gwClient.AddListener(proto.PacketKindConstraintsRequest, func(in []byte) ([]byte, error) {
		var constraintsRequest proto.PacketConstraintsRequest
		if err := proto.DecodeSnappy(in, &constraintsRequest); err != nil {
			return nil, err
		}

		constraints := make([]*agent.Constraint, 0, len(constraintsRequest.Constraints))
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
				Code:        c.Code,
				Description: c.Description,
				HowToSolve:  c.HowToSolve,
				UpdatedAt:   c.UpdatedAt,
				CategoryId:  c.CategoryId,
				Severity:    c.Severity,
			}
			constraints = append(constraints, constraint)
		}
		errMap := g.addConstraints(constraints)
		if errMap != nil && len(errMap) > 0 {
			// TODO: Send errors and ids in constraint response
			for id, err := range errMap {
				logger.Errorw("Couldn't add constraint", "error", err, "constraint-id", id)
			}
		}

		return proto.EncodeSnappy(proto.PacketConstraintsResponse{})
	})
}

func (g *MagalixGateway) SetAuditCommandHandler(handler agent.AuditCommandHandler) {
	if handler == nil {
		panic("audi command handler is nil")
	}
	g.handleAuditCommand = handler
	g.gwClient.AddListener(proto.PacketKindAuditCommand, func(in []byte) ([]byte, error) {
		// Packet is empty so no need to decode

		err := g.handleAuditCommand()
		if err != nil {
			logger.Errorw("Couldn't run audit", "error", err)
		}

		return nil, err
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
			CategoryID:    r.CategoryID,
			Severity:      r.Severity,
			Description:   r.Description,
			HowToSolve:    r.HowToSolve,
			Msg:           r.Msg,
			EntityName:    r.EntityName,
			EntityKind:    r.EntityKind,
			NamespaceName: r.NamespaceName,
			ParentName:    r.ParentName,
			ParentKind:    r.ParentKind,
			NodeIP:        r.NodeIP,
			EntitySpec:    r.EntitySpec,
		}

		switch r.Status {
		case agent.AuditResultStatusViolating:
			item.Status = proto.AuditResultStatusViolating
		case agent.AuditResultStatusCompliant:
			item.Status = proto.AuditResultStatusCompliant
		case agent.AuditResultStatusIgnored:
			item.Status = proto.AuditResultStatusIgnored
		case agent.AuditResultStatusError:
			item.Status = proto.AuditResultStatusError
		}

		items = append(items, &item)
	}
	logger.Infof("Sending %d audit results", len(auditResult))
	packet := proto.PacketAuditResultRequest{
		Items:     items,
		Timestamp: time.Now().UTC(),
	}
	g.gwClient.Pipe(client.Package{
		Kind:        proto.PacketKindAuditResultRequest,
		ExpiryTime:  utils.After(auditResultPacketExpireAfter),
		ExpiryCount: auditResultPacketExpireCount,
		Priority:    auditResultPacketPriority,
		Retries:     auditResultPacketRetries,
		Data:        packet,
	})

}
