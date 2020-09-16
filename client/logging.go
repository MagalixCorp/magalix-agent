package client

import (
	"time"

	"github.com/MagalixCorp/magalix-agent/v2/proto"
	"github.com/MagalixCorp/magalix-agent/v2/utils"
)

func (c *Client) Write(p []byte) (n int, err error) {
	if c.shouldSendLogs {
		c.logBuffer <- proto.PacketLogItem{
			Date: time.Now(),
			Data: string(p),
		}
	}
	return len(p), nil
}

func (c *Client) Sync() error {
	for log := range c.logBuffer {
		pkg := Package{
			Kind:        proto.PacketKindLogs,
			ExpiryTime:  utils.After(10 * time.Minute),
			ExpiryCount: 2,
			Priority:    9,
			Retries:     4,
			Data:        log,
		}
		c.Pipe(pkg)
	}
	return nil
}
