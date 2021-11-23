package client

import (
	"fmt"
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
	client.logM.Lock()
	defer client.logM.Unlock()

	if client.shouldSendLogs {
		client.logBuffer = append(client.logBuffer, proto.PacketLogItem{
			Date: time.Now(),
			Data: string(p),
		})

		if len(client.logBuffer) == logBatchSize {
			err = client.flushLogs()
		}
	}
	return len(client.logBuffer), err
}

func (client *Client) flushLogs() error {
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

	err := client.Pipe(pkg)
	if err != nil {
		return fmt.Errorf("error when sending logs, %w", err)
	}
	return nil
}

func (client *Client) Sync() error {
	// TODO: Should actually wait till logs are sent
	return client.flushLogs()
}
