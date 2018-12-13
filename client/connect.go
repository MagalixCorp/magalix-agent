package client

import (
	"os"
	"strings"
	"sync"
	"time"
)

func (client *Client) onConnect() error {
	expire := time.Now().Add(time.Minute * 10)
	for try := 0; try < 1000; try++ {
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
	client.authorized = false
}

// Connect starts the client
func (client *Client) Connect() error {
	oc := client.onConnect
	odc := client.onDisconnect
	client.channel.SetHooks(&oc, &odc)
	go client.channel.Listen()
	return nil
}

// IsReady returns true if the agent is connected and authenticated
func (client *Client) IsReady() bool {
	return client.authorized
}
