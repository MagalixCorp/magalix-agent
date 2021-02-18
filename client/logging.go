package client

import (
	"time"

	"github.com/MagalixCorp/magalix-agent/v3/proto"
	"github.com/MagalixCorp/magalix-agent/v3/utils"
)

const (
	logBatchSize    = 5
	logExpiryPeriod = 10 * time.Minute
	logExpiryCount  = 2
	logPriority     = 9
	logRetryCount   = 4
)

func (client *Client) Write(p []byte) (n int, err error) {
	client.blockedM.Lock()
	defer client.blockedM.Unlock()

	if client.shouldSendLogs {
		client.logBuffer = append(client.logBuffer, proto.PacketLogItem{
			Date: time.Now(),
			Data: string(p),
		})

		if len(client.logBuffer) == logBatchSize {
			client.flushLogs()
		}
	}
	return len(client.logBuffer), nil
}

func (client *Client) flushLogs() {
	payload := make(proto.PacketLogs, len(client.logBuffer))
	copy(payload, client.logBuffer)
	client.logBuffer = make(proto.PacketLogs, 0, logBatchSize)
	pkg := Package{
		Kind:        proto.PacketKindLogs,
		ExpiryTime:  utils.After(logExpiryPeriod),
		ExpiryCount: logExpiryCount,
		Priority:    logPriority,
		Retries:     logRetryCount,
		Data:        payload,
	}

	client.Pipe(pkg)
}

func (client *Client) Sync() error {
	// TODO: Should actually wait till logs are sent
	client.flushLogs()
	return nil
}
