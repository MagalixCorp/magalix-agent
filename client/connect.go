package client

import (
	"os"
	"strings"
	"sync"
	"time"
)

func (client *Client) onConnect() error {
	client.connected = true
	expire := time.Now().Add(time.Minute * 10)
	for try := 0; try < 1000; try++ {
		if !client.connected {
			return nil
		}
		err := client.hello()
		if err != nil {
			client.Errorf(
				err,
				"unable to verify protocol version with remote server",
			)
			if time.Now().After(expire) || strings.Contains(err.Error(), "unsupported version") {
				break
			}
			continue
		}

		err = client.authorize()
		if err != nil {
			client.Errorf(
				err,
				"unable to authorize client",
			)
			continue
		}
		client.authorized = true

		client.blockedM.Lock()
		defer client.blockedM.Unlock()
		client.blocked.Range(func(k, v interface{}) bool {
			k.(chan struct{}) <- struct{}{}
			return true
		})
		client.blocked = sync.Map{}

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
func (client *Client) Connect() error {
	go client.StartWatchdog()
	oc := client.onConnect
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
		if (client.lastSent == time.Time{}) {
			if startTime.Add(1 * time.Minute).Before(time.Now()) {
				break
			}
		} else if client.lastSent.Add(1 * time.Minute).Before(time.Now()) {
			break
		}
		time.Sleep(time.Minute)
	}
	os.Exit(120)
}
