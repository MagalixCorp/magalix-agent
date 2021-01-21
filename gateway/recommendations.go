package gateway

import (
	"fmt"
	"time"

	"github.com/MagalixCorp/magalix-agent/v2/client"
	"github.com/MagalixCorp/magalix-agent/v2/proto"
	"github.com/MagalixCorp/magalix-agent/v2/utils"
	"github.com/MagalixTechnologies/core/logger"
	"github.com/open-policy-agent/frameworks/constraint/pkg/types"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	recPacketExpireAfter = 30 * time.Minute
	recPacketExpireCount = 0
	recPacketPriority    = 1
	recPacketRetries     = 5
)

func (g *MagalixGateway) SendRecs(recommendations []*types.Result) error {
	items := make([]proto.PacketRecommendationItem, len(recommendations))
	for _, r := range recommendations {
		resource, ok := r.Resource.(*unstructured.Unstructured)
		if !ok {
			err := fmt.Errorf("type mismatch expected unstructred")
			logger.Errorw("Couldn't get resource from audit result", "error", err)
			return err
		}
		enforced := false
		if r.EnforcementAction == "deny" {
			enforced = true
		}
		item := proto.PacketRecommendationItem{
			Constraint: r.Constraint,
			Resource:   resource,
			Message:    r.Msg,
			Enforced:   enforced,
		}

		items = append(items, item)

	}
	logger.Infof("Sending  %d recommendations", len(recommendations))
	packet := proto.PacketRecommendationItemRequest{
		Items:     items,
		Timestamp: time.Now().UTC(),
	}
	g.gwClient.Pipe(client.Package{
		Kind:        proto.PacketKindRecommendationItemRequest,
		ExpiryTime:  utils.After(recPacketExpireAfter),
		ExpiryCount: recPacketExpireCount,
		Priority:    recPacketPriority,
		Retries:     recPacketRetries,
		Data:        packet,
	})
	return nil
}
