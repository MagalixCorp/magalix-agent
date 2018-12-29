package client

import (
	"github.com/MagalixCorp/magalix-agent/utils"
	"sync"
	"time"

	"github.com/MagalixCorp/magalix-agent/proto"
	"github.com/kovetskiy/lorg"
	structured "github.com/reconquest/cog"
	"github.com/reconquest/karma-go"
)

var _ structured.Sender = ((*Client)(nil)).sendLogs

func (client *Client) sendLogs(
	level lorg.Level, hierarchy karma.Hierarchical,
) error {
	client.logsQueue <- proto.PacketLogItem{
		Level: level,
		Date:  time.Now().UTC(),
		Data:  hierarchy.String(),
	}

	return nil
}

func (client *Client) initLogger() {
	client.Logger = client.parentLogger.NewChild()

	// instead of default displayer we will display to stderr using global
	// logger.
	// Note that parentLogger is the global stderr
	client.Logger.SetDisplayer(client.parentLogger.Display)

	if client.shouldSendLogs {
		// as sender we will use client's packet logs
		client.Logger.SetSender(client.sendLogs)
		client.initLogsQueue()
	}

	client.Logger.Log.SetExiter(func(int) {
		return
	})
}

func (client *Client) initLogsQueue() {
	client.logsQueue = make(chan proto.PacketLogItem, logsQueueSize)
	client.logsQueueWorker = &sync.WaitGroup{}

	go client.watchLogsQueue()
}

func (client *Client) watchLogsQueue() {
	client.logsQueueWorker.Add(1)
	defer client.logsQueueWorker.Done()

	client.parentLogger.Trace("{logs:queue} logs queue watcher started")

	var fatal bool

	// retry for 5 times then drop the packet
	backoff := utils.Backoff{
		Sleep:      client.timeouts.protoBackoff,
		MaxRetries: 5,
	}

	for {
		logs := proto.PacketLogs{}
		t := time.Now()
		for {
			select {
			case log := <-client.logsQueue:
				logs = append(logs, log)
				if !fatal {
					fatal = log.Level == lorg.LevelFatal
				}
				if fatal || time.Now().Sub(t) > time.Minute {
					goto flush
				}
			case <-time.After(time.Minute):
				goto flush
			}
		}

	flush:
		// NOTE: don't use client.WithBackoffLimit as it uses the client
		// NOTE: logger which we send its logs.
		// NOTE: If used and sending logs fails, we may be trapped
		// NOTE: in a deadlock because the logsQueue will be full.
		utils.WithBackoff(
			func() error {
				client.parentLogger.Tracef(nil, "sending %v log entries", len(logs))

				var response []byte
				err := client.Send(proto.PacketKindLogs, logs, &response)
				if err != nil {
					return karma.Format(
						err,
						"unable to send logs packet",
					)
				}

				return nil
			},
			backoff,
			nil,
		)

		if fatal {
			client.Done(1)
			goto done
		}

	}
done:
}
