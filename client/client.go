package client

import (
	"net/url"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/MagalixTechnologies/agent/proto"
	"github.com/MagalixTechnologies/agent/utils"
	"github.com/MagalixTechnologies/channel"
	"github.com/MagalixTechnologies/log-go"
	"github.com/MagalixTechnologies/uuid-go"
	"github.com/reconquest/karma-go"
	"github.com/reconquest/sign-go"
)

const (
	ProtocolMajorVersion = 1
	ProtocolMinorVersion = 3

	logsQueueSize = 1024
)

type timeouts struct {
	protoHandshake time.Duration
	protoWrite     time.Duration
	protoRead      time.Duration
	protoReconnect time.Duration
	protoBackoff   time.Duration
}

// Client agent gateway client
type Client struct {
	*log.Logger

	parentLogger *log.Logger

	address   string
	AccountID uuid.UUID
	ClusterID uuid.UUID
	secret    []byte

	channel *channel.Client

	authorized bool

	logsQueue       chan proto.PacketLogItem
	logsQueueWorker *sync.WaitGroup

	exit chan int

	// for thread blocked on connection
	blocked  sync.Map
	blockedM sync.Mutex

	timeouts timeouts
}

// newClient creates a new client
func newClient(
	address string,
	accountID uuid.UUID,
	clusterID uuid.UUID,
	secret []byte,
	timeouts timeouts,
	parentLogger *log.Logger,
) *Client {
	url, err := url.Parse(address)
	if err != nil {
		panic(err)
	}
	client := &Client{
		parentLogger: parentLogger,

		address:   address,
		AccountID: accountID,
		ClusterID: clusterID,
		secret:    secret,

		channel: channel.NewClient(*url, channel.ChannelOptions{
			ProtoHandshake: timeouts.protoHandshake,
			ProtoWrite:     timeouts.protoWrite,
			ProtoRead:      timeouts.protoRead,
			ProtoReconnect: timeouts.protoReconnect,
		}),
		exit: make(chan int, 1),

		blocked:  sync.Map{},
		blockedM: sync.Mutex{},

		timeouts: timeouts,
	}

	client.initLogger()
	client.initLogsQueue()

	return client
}

// WaitForConnection waits for an established connection with the agent gateway
// it blocks until the agent gateway is connected and the agent is authenticated
// it takes a timeout parameter to return if not connected
// returns true if connected false if timeout occurred
// Example:
//   WaitForConnection(time.Second * 10)
func (client *Client) WaitForConnection(timeout time.Duration) bool {
	c := make(chan struct{})
	defer func() {
		client.blocked.Delete(c)
		close(c)
	}()
	func() {
		client.blockedM.Lock()
		defer client.blockedM.Unlock()
		client.blocked.Store(c, struct{}{})
	}()
	select {
	case <-c:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (client *Client) WithBackoff(fn func() error) {
	client.withBackoffLimit(fn, int(^uint(0)>>1))
}

func (client *Client) withBackoffLimit(fn func() error, limit int) {
	for try := 0; try < limit; try++ {
		err := fn()
		if err == nil {
			break
		}

		// 300ms -> 600ms -> [...] -> 3000ms -> 300ms
		timeout := client.timeouts.protoBackoff * time.Duration(try%10+1)

		client.Errorf(
			karma.Describe("retry", try).Reason(err),
			"unhandled error occurred, retrying after %s",
			timeout,
		)

		time.Sleep(timeout)
	}
}

// Send sends a packet to the agent-gateway
// It tries to send it, if it failed due to the agent-gateway not connected it waits
// for connection before trying again
// it uses the default proto encoding to encode and decode in/out parameters
func (client *Client) Send(kind proto.PacketKind, in interface{}, out interface{}) error {
	client.parentLogger.Debugf(karma.Describe("kind", kind), "sending package")
	defer client.parentLogger.Debugf(karma.Describe("kind", kind), "package sent")
	req, err := proto.Encode(in)
	if err != nil {
		return err
	}
	res, err := client.channel.Send(kind.String(), req)
	if err != nil {
		// TODO: define errors in the channel package
		if err.Error() == "client not found" {
			client.WaitForConnection(time.Minute)
			res, err = client.channel.Send(kind.String(), req)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	}
	return proto.Decode(res, out)
}

// AddListener adds a listener for a specific packet kind
func (client *Client) AddListener(kind proto.PacketKind, listener func(in []byte) ([]byte, error)) {
	if err := client.channel.AddListener(kind.String(), listener); err != nil {
		panic(err)
	}
}

func InitClient(
	args map[string]interface{},
	accountID, clusterID uuid.UUID,
	secret []byte,
	parentLogger *log.Logger,
) (*Client, error) {
	client := newClient(
		args["--gateway"].(string), accountID, clusterID, secret,
		timeouts{
			protoHandshake: utils.MustParseDuration(args, "--timeout-proto-handshake"),
			protoWrite:     utils.MustParseDuration(args, "--timeout-proto-write"),
			protoRead:      utils.MustParseDuration(args, "--timeout-proto-read"),
			protoReconnect: utils.MustParseDuration(args, "--timeout-proto-reconnect"),
			protoBackoff:   utils.MustParseDuration(args, "--timeout-proto-backoff"),
		},
		parentLogger,
	)
	go sign.Notify(func(os.Signal) bool {
		if !client.IsReady() {
			return true
		}

		client.Infof(nil, "got SIGHUP signal, sending ping-pong")
		client.WithBackoff(func() error {
			err := client.ping()
			if err != nil {
				client.Errorf(err, "unable to send ping-pong request to gateway")
				return err
			}

			return nil
		})

		return true
	}, syscall.SIGHUP)

	err := client.Connect()

	return client, err
}
