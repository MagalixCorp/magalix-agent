package client

import (
	"time"

	"github.com/MagalixCorp/magalix-agent/v2/proto"
	"github.com/MagalixCorp/magalix-agent/v2/utils"
	"github.com/MagalixTechnologies/core/logger"
)

func (c *Client) Write(p []byte) (n int, err error) {
	c.blockedM.Lock()
	defer c.blockedM.Unlock()

	c.logBuffer = append(c.logBuffer, proto.PacketLogItem{
		Date: time.Now(),
		Data: string(p),
	})

	if len(c.logBuffer) == 5 {
		payload := make(proto.PacketLogs, len(c.logBuffer))
		copy(payload, c.logBuffer)
		c.logBuffer = make(proto.PacketLogs, 0, 5)
		pkg := Package{
			Kind:        proto.PacketKindLogs,
			ExpiryTime:  utils.After(10 * time.Minute),
			ExpiryCount: 2,
			Priority:    9,
			Retries:     4,
			Data:        payload,
		}

		c.Pipe(pkg)
	}
	return len(c.logBuffer), nil
}

func (c *Client) Sync() error {
	return nil
}

// SetLogLevel sets log level for the agent
func (c *Client) SetLogLevel(level string) bool {

	ok := true
	switch level {
	case "info":
		logger.ConfigWriterSync(logger.InfoLevel, c)
	case "debug":
		logger.ConfigWriterSync(logger.DebugLevel, c)
	case "warn":
		logger.ConfigWriterSync(logger.WarnLevel, c)
	case "error":
		logger.ConfigWriterSync(logger.ErrorLevel, c)
	default:
		ok = false
	}
	logger.WithGlobal("accountID", c.AccountID, "clusterID", c.ClusterID)
	return ok
}
