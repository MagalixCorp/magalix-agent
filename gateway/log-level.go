package gateway

import (
	"github.com/MagalixCorp/magalix-agent/v3/agent"
	"github.com/MagalixCorp/magalix-agent/v3/proto"
	"github.com/MagalixTechnologies/core/logger"
)

func (g *MagalixGateway) SetChangeLogLevelHandler(handler agent.ChangeLogLevelHandler) {
	if handler == nil {
		panic("change log level handler is nil")
	}
	g.changeLogLevel = handler
	g.gwClient.AddListener(proto.PacketKindLogLevel, func(in []byte) ([]byte, error) {
		var logLevel proto.PacketLogLevel
		if err := proto.DecodeSnappy(in, &logLevel); err != nil {
			logger.Error("Failed to decode log level packet")
			return nil, err
		}

		err := g.changeLogLevel(&agent.LogLevel{
			Level: logLevel.Level,
		})

		return nil, err
	})
}
