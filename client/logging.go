package client

import (
	"time"

	"github.com/MagalixCorp/magalix-agent/v2/proto"
	"github.com/MagalixCorp/magalix-agent/v2/utils"
)

const logBatchSize = 5

func (c *Client) Write(p []byte) (n int, err error) {
	c.blockedM.Lock()
	defer c.blockedM.Unlock()

	if c.shouldSendLogs {
		c.logBuffer = append(c.logBuffer, proto.PacketLogItem{
			Date: time.Now(),
			Data: string(p),
		})

		if len(c.logBuffer) == logBatchSize {
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
	}
	return len(c.logBuffer), nil
}

func (c *Client) Sync() error {
	return nil
}
