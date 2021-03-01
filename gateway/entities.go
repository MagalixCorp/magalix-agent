package gateway

import (
	"github.com/MagalixCorp/magalix-agent/v3/agent"
	"github.com/MagalixCorp/magalix-agent/v3/client"
	"github.com/MagalixCorp/magalix-agent/v3/proto"
	"github.com/MagalixCorp/magalix-agent/v3/utils"
	"github.com/MagalixTechnologies/core/logger"
	"time"
)

const (
	deltasPacketExpireAfter = 30 * time.Minute
	deltasPacketExpireCount = 0
	deltasPacketPriority    = 1
	deltasPacketRetries     = 5

	resyncPacketExpireAfter = time.Hour
	resyncPacketExpireCount = 2
	resyncPacketPriority    = 0
	resyncPacketRetries     = 5
)

func (g *MagalixGateway) SendEntitiesDeltas(deltas []*agent.Delta) error {
	g.sendDeltas(deltas)
	return nil
}

func (g *MagalixGateway) SendEntitiesResync(resync *agent.EntitiesResync) error {
	g.sendEntitiesResync(resync)
	return nil
}

func (g *MagalixGateway) sendDeltas(deltas []*agent.Delta) {
	if len(deltas) == 0 {
		return
	}
	logger.Info("Sending deltas")
	items := make([]proto.PacketEntityDelta, len(deltas))
	i := 0
	for _, item := range deltas {
		items[i] = proto.PacketEntityDelta{
			Gvrk: proto.GroupVersionResourceKind{
				GroupVersionResource: item.Gvrk.GroupVersionResource,
				Kind:                 item.Gvrk.Kind,
			},
			DeltaKind: proto.EntityDeltaKind(item.Kind),
			Data:      item.Data,
			Parent:    getParentControllers(item.Parent),
			Timestamp: item.Timestamp,
		}
		i++
	}
	packet := proto.PacketEntitiesDeltasRequest{
		Items:     items,
		Timestamp: time.Now().UTC(),
	}
	g.gwClient.Pipe(client.Package{
		Kind:        proto.PacketKindEntitiesDeltasRequest,
		ExpiryTime:  utils.After(deltasPacketExpireAfter),
		ExpiryCount: deltasPacketExpireCount,
		Priority:    deltasPacketPriority,
		Retries:     deltasPacketRetries,
		Data:        packet,
	})
	logger.Infof("%d deltas sent", len(deltas))
}

func getParentControllers(parent *agent.ParentController) *proto.ParentController {
	if parent == nil {
		return nil
	}

	return &proto.ParentController{
		Kind:       parent.Kind,
		Name:       parent.Name,
		APIVersion: parent.APIVersion,
		IsWatched:  parent.IsWatched,
		Parent:     getParentControllers(parent.Parent),
	}
}

func (g *MagalixGateway) sendEntitiesResync(resync *agent.EntitiesResync) {
	packet := proto.PacketEntitiesResyncRequest{
		Timestamp: resync.Timestamp,
		Snapshot:  make(map[string]proto.PacketEntitiesResyncItem),
	}

	for key, item := range resync.Snapshot {
		packet.Snapshot[key] = proto.PacketEntitiesResyncItem{
			Gvrk: proto.GroupVersionResourceKind{
				GroupVersionResource: item.Gvrk.GroupVersionResource,
				Kind:                 item.Gvrk.Kind,
			},
			Data: item.Data,
		}
	}

	g.gwClient.Pipe(client.Package{
		Kind:        proto.PacketKindEntitiesResyncRequest,
		ExpiryTime:  utils.After(resyncPacketExpireAfter),
		ExpiryCount: resyncPacketExpireCount,
		Priority:    resyncPacketPriority,
		Retries:     resyncPacketRetries,
		Data:        packet,
	})
}
