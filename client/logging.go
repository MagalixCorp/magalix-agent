package client

import (
	"time"

	"github.com/MagalixCorp/magalix-agent/v2/proto"
	"github.com/MagalixCorp/magalix-agent/v2/utils"
)

const logBatchSize = 5

func (client *Client) Write(p []byte) (n int, err error) {
	client.blockedM.Lock()
	defer client.blockedM.Unlock()

	if client.shouldSendLogs {
		client.logBuffer = append(client.logBuffer, proto.PacketLogItem{
			Date: time.Now(),
			Data: string(p),
		})

		if len(client.logBuffer) == logBatchSize {
			payload := make(proto.PacketLogs, len(client.logBuffer))
			copy(payload, client.logBuffer)
			client.logBuffer = make(proto.PacketLogs, 0, 5)
			pkg := Package{
				Kind:        proto.PacketKindLogs,
				ExpiryTime:  utils.After(10 * time.Minute),
				ExpiryCount: 2,
				Priority:    9,
				Retries:     4,
				Data:        payload,
			}

			client.Pipe(pkg)
		}
	}
	return len(client.logBuffer), nil
}

func (client *Client) Sync() error {
	return nil
}
