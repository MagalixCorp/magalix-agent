package client

import (
	"context"
	"fmt"
	"github.com/reconquest/sign-go"
	"golang.org/x/sync/errgroup"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/MagalixTechnologies/channel"
	"github.com/MagalixTechnologies/core/logger"
)

const watchdogInterval = time.Minute

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
			logger.Errorw("unable to verify protocol version with remote server", "error", err)
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
	client.blockedM.Lock()
	defer client.blockedM.Unlock()
	client.connected = false
	client.authorized = false
}

// Connect starts the client
func (client *Client) Connect(ctx context.Context, connect chan bool) error {
	oc := func() error { return client.onConnect(connect) }
	odc := client.onDisconnect
	client.channel.SetHooks(&oc, &odc)
	eg, egCtx := errgroup.WithContext(ctx)

	// TODO: find a better way to handle this
	eg.Go(func() error {
		sign.Notify(func(os.Signal) bool {
			if !client.IsReady() {
				return true
			}

			logger.Info("got SIGHUP signal and not connected, pinging the agent gateway")
			client.WithBackoff(func() error {
				err := client.ping()
				if err != nil {
					logger.Errorw("unable to send ping-pong request to gateway", "error", err)
					return err
				}

				return nil
			})

			return true
		}, syscall.SIGHUP)
		return nil
	})

	eg.Go(func() error { return client.StartWatchdog(egCtx) })
	// TODO: Refactor channel package to use a context for managing go routines
	go client.channel.Listen()
	client.pipe.Start(10)
	client.pipeStatus.Start( 1)
	return eg.Wait()
}

// IsReady returns true if the agent is connected and authenticated
func (client *Client) IsReady() bool {
	return client.authorized
}

func (client *Client) StartWatchdog(ctx context.Context) error {
	startTime := time.Now()
	client.watchdogTicker = time.NewTicker(watchdogInterval)
	msg := "nothing sent for more than 10 minutes"
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-client.watchdogTicker.C:
			client.blockedM.Lock()
			{
				// it didn't send anything before
				if (client.lastSent == time.Time{}) {
					if startTime.Add(10 * time.Minute).Before(time.Now()) {
						return fmt.Errorf(msg)
					}
				} else if client.lastSent.Add(10 * time.Minute).Before(time.Now()) {
					return fmt.Errorf(msg)
				}
			}
			client.blockedM.Unlock()
		}
	}
}
