package gateway

import (
	"github.com/MagalixCorp/magalix-agent/v3/agent"
	"github.com/MagalixCorp/magalix-agent/v3/proto"
)

func (g *MagalixGateway) SetRestartHandler(handler agent.RestartHandler) {
	if handler == nil {
		panic("restart handler is nil")
	}
	g.triggerRestart = handler
	g.gwClient.AddListener(proto.PacketKindRestart, func(in []byte) ([]byte, error) {
		var restart proto.PacketRestart
		if err := proto.DecodeSnappy(in, &restart); err != nil {
			return nil, err
		}

		err := g.triggerRestart()

		return nil, err
	})
}