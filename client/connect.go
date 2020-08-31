package client

import (
	"os"
	"strings"
	"sync"
	"time"

	"github.com/MagalixTechnologies/channel"
	"github.com/MagalixTechnologies/core/logger"
)

func (client *Client) onConnect(connected chan bool) error {
	client.blockedM.Lock()
	client.connected = true
	client.blockedM.Unlock()

	expire := time.Now().Add(time.Minute * 10)
	_ = client.WithBackoffLimit(func() error {

		if !client.connected {
			return nil
		}

		err := client.hello()
		if err != nil {
			client.Errorf(err, "unable to verify protocol version with remote server")
			if time.Now().After(expire) || strings.Contains(err.Error(), "unsupported version") {
				return nil // breaking condition for backoff
			}
			return err // continue condition for backoff
		}

		err = client.authorize()

		if err != nil {
			connectionError, ok := err.(*channel.ProtocolError)

			if ok {
				if connectionError.Code == 404 {
					// TODO: Remove this loop once we get permission to delete the agent
					for {
						time.Sleep(time.Hour * 8760)
					}

				}
			}

			logger.Errorw(
				"unable to authorize client",
				"error", err,
			)
			return err // continue condition for backoff
		}

		client.authorized = true
		connected <- true

		client.blockedM.Lock()
		defer client.blockedM.Unlock()
		client.blocked.Range(func(k, v interface{}) bool {
			k.(chan struct{}) <- struct{}{}
			return true
		})

		client.blocked = sync.Map{}

		return nil
	}, 100)

	if client.authorized {
		return nil
	}

	// if it fails to connect for time
	os.Exit(122)
	return nil
}

func (client *Client) onDisconnect() {
	client.connected = false
	client.authorized = false
}

// Connect starts the client
func (client *Client) Connect(connect chan bool) error {
	go client.StartWatchdog()
	oc := func() error { return client.onConnect(connect) }
	odc := client.onDisconnect
	client.channel.SetHooks(&oc, &odc)
	go client.channel.Listen()
	client.pipe.Start(10)
	client.pipeStatus.Start(1)
	return nil
}

// IsReady returns true if the agent is connected and authenticated
func (client *Client) IsReady() bool {
	return client.authorized
}

func (client *Client) StartWatchdog() {
	startTime := time.Now()
	for {
		// it didn't sent anything before
		client.blockedM.Lock()
		{
			if (client.lastSent == time.Time{}) {
				if startTime.Add(10 * time.Minute).Before(time.Now()) {
					break
				}
			} else if client.lastSent.Add(10 * time.Minute).Before(time.Now()) {
				break
			}
		}
		client.blockedM.Unlock()
		time.Sleep(time.Minute)
	}
	os.Exit(120)
}
