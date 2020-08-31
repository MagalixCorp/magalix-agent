package client

import (
	"time"

	"github.com/MagalixCorp/magalix-agent/v2/proto"
	"github.com/MagalixCorp/magalix-agent/v2/utils"
)

func (c *Client) Write(p []byte) (n int, err error) {
	if c.shouldSendLogs {
		lg := proto.PacketLogItem{
			Date: time.Now(),
			Data: p,
		}

		c.Pipe(Package{
			Kind:        proto.PacketKindLogs,
			ExpiryTime:  utils.After(10 * time.Minute),
			ExpiryCount: 2,
			Priority:    9,
			Retries:     4,
			Data:        lg,
		})
	}
	return len(p), nil
}

func (c *Client) Sync() error {
	// nothing to sync for websocket
	return nil
}
