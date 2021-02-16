package gateway

import (
	"math"
	"time"

	"github.com/MagalixCorp/magalix-agent/v2/kuber"
	"github.com/MagalixTechnologies/uuid-go"

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

func (g *MagalixGateway) SetConstraintsHandler(handler agent.ConstraintsHandler) {
	if handler == nil {
		panic("constraints handler is nil")
	}
	g.addConstraints = handler
	//g.gwClient.AddListener(proto.PacketKindConstraintsRequest, func(in []byte) ([]byte, error) {
	//	var constraintsRequest proto.PacketConstraintsRequest
	//	if err := proto.DecodeSnappy(in, &constraintsRequest); err != nil {
	//		return nil, err
	//	}
	//
	//	constraints := make([]*agent.Constraint, 0, len(constraintsRequest.Constraints))
	//	for _, c := range constraintsRequest.Constraints {
	//		constraints = append(constraints, &agent.Constraint{
	//			Id:           c.Id,
	//			TemplateId:   c.TemplateId,
	//			AccountId:    c.AccountId,
	//			ClusterId:    c.ClusterId,
	//			Name:         c.Name,
	//			TemplateName: c.TemplateName,
	//			Parameters:   c.Parameters,
	//			Match:        agent.Match{
	//				Namespaces: c.Match.Namespaces,
	//				Kinds:      c.Match.Kinds,
	//			},
	//			Code:         c.Code,
	//			UpdatedAt:    c.UpdatedAt,
	//		})
	//	}
	//	err := g.addConstraints(constraints)
	//	if err != nil {
	//		logger.Errorf("couldn't add constraint. %w", err)
	//	}
	//
	//	return proto.EncodeSnappy(proto.PacketConstraintsResponse{})
	//})
	go func() {
		id, _ := uuid.FromString("c45c7866-7b53-4a76-9255-bad4f1b85304")
		templateId, _ := uuid.FromString("0a0ce87a-2dc9-460b-a5b7-1511b0c02ce5")
		accountId, _ := uuid.FromString("5e95e8e3-c411-429e-8c50-2fa9a3806a21")

		code := `package k8srequiredlabels
violation[{"msg": msg, "details": {"missing_labels": missing}}] {
  provided := {label | input.review.object.metadata.labels[label]}
  required := {label | label := input.parameters.labels[_]}
  missing := required - provided
  msg := sprintf("you must provide labels: %v %v %v", [provided, required, missing])
  }`
		updatedAt, _ := time.Parse(time.RFC3339, "2021-02-11T16:02:52.738Z")

		logger.Info("===WAITING TO SEND CONSTRAINT")
		time.Sleep(15 * time.Second)
		g.addConstraints([]*agent.Constraint{
			{
				Id:           id,
				TemplateId:   templateId,
				AccountId:    accountId,
				Name:         "testtheredirection",
				TemplateName: "tgreedlongdescriptiontemplateandhowtoresolveupdate",
				Parameters: map[string]interface{}{
					"lables": []interface{}{
						"nonexistinglabel",
					},
				},
				Match: agent.Match{
					Namespaces: []interface{}{"gatekeeper-system"},
					Kinds:      []string{kuber.Deployments.Kind},
				},
				Code:      code,
				UpdatedAt: updatedAt,
			},
		})
		logger.Info("===CONSTRAINT SENT")
	}()
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
