package client

import (
	"net/url"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/MagalixCorp/magalix-agent/v2/proto"
	"github.com/MagalixCorp/magalix-agent/v2/utils"
	"github.com/MagalixTechnologies/channel"
	"github.com/MagalixTechnologies/log-go"
	"github.com/MagalixTechnologies/uuid-go"
	"github.com/reconquest/karma-go"
	"github.com/reconquest/sign-go"
)

const (
	ProtocolMajorVersion = 2
	ProtocolMinorVersion = 4

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
	version   string
	startID   string
	AccountID uuid.UUID
	ClusterID uuid.UUID
	secret    []byte

	packetV2Enabled bool

	channel *channel.Client

	connected  bool
	authorized bool

	shouldSendLogs  bool
	logsQueue       chan proto.PacketLogItem
	logsQueueWorker *sync.WaitGroup

	exit chan int

	// for thread blocked on connection
	blocked  sync.Map
	blockedM sync.Mutex

	timeouts timeouts

	lastSent time.Time

	pipe       *Pipe
	pipeStatus *Pipe
}

// newClient creates a new client
func newClient(
	address string,
	version string,
	startID string,
	accountID uuid.UUID,
	clusterID uuid.UUID,
	secret []byte,
	timeouts timeouts,
	parentLogger *log.Logger,
	shouldSendLogs bool,
	packetV2Enabled bool,
) *Client {
	gwUrl, err := url.Parse(address)
	if err != nil {
		panic(err)
	}

	if gwUrl.Scheme == "ws" {
		gwUrl.Scheme = "wss"
	}

	address = gwUrl.String()

	client := &Client{
		parentLogger: parentLogger,

		address:         address,
		version:         version,
		startID:         startID,
		AccountID:       accountID,
		ClusterID:       clusterID,
		secret:          secret,
		shouldSendLogs:  shouldSendLogs,
		packetV2Enabled: packetV2Enabled,

		channel: channel.NewClient(*gwUrl, channel.ChannelOptions{
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

	client.pipe = NewPipe(client, client.parentLogger)
	client.pipeStatus = NewPipe(client, client.parentLogger)

	client.initLogger()

	return client
}

// WaitForConnection waits for an established connection with the agent gateway
// it blocks until the agent gateway is connected and the agent is authenticated
// it takes a timeout parameter to return if not connected
// returns true if connected false if timeout occurred
// Example:
//   WaitForConnection(time.Second * 10)
func (client *Client) WaitForConnection(timeout time.Duration) bool {
	if client.authorized {
		return true
	}
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
	_ = client.WithBackoffLimit(fn, int(^uint(0)>>1))
}

func (client *Client) WithBackoffLimit(fn func() error, limit int) error {
	var err error
	for try := 0; try < limit; try++ {
		err = fn()
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

		if try+1 < limit {
			time.Sleep(timeout)
		}
	}
	if err != nil {
		client.Errorf(
			karma.Describe("limit", limit).Reason(err),
			"unhandled error occurred, retires limit %v is exceeded",
			limit,
		)
	}
	return err
}

// send sends a packet to the agent-gateway
// it uses the default proto encoding to encode and decode in/out parameters
func (client *Client) send(kind proto.PacketKind, in interface{}, out interface{}) error {
	var (
		req []byte
		err error
	)
	if kind == proto.PacketKindHello {
		req, err = proto.EncodeGOB(in)
		if err != nil {
			return err
		}
	} else {
		req, err = proto.EncodeSnappy(in)
		if err != nil {
			return err
		}
	}
	res, err := client.channel.Send(kind.String(), req)
	if err != nil {
		return err
	}
	client.lastSent = time.Now()

	if out == nil {
		return nil
	}

	if kind == proto.PacketKindHello {
		return proto.DecodeGOB(res, out)
	} else {
		return proto.DecodeSnappy(res, out)
	}
}

// Send sends a packet to the agent-gateway if there is an established connection it internally uses client.send
func (client *Client) Send(kind proto.PacketKind, in interface{}, out interface{}) error {
	client.parentLogger.Debugf(karma.Describe("kind", kind), "sending package")
	defer client.parentLogger.Debugf(karma.Describe("kind", kind), "package sent")
	client.WaitForConnection(time.Minute)
	return client.send(kind, in, out)
}

// PipeStatus send status packages to the agent-gateway with defined priorities and expiration rules
// TODO remove
func (client *Client) PipeStatus(pack Package) {
	if client.pipeStatus == nil {
		panic("client pipeStatus not defined")
	}
	i := client.pipeStatus.Send(pack)
	if i > 0 {
		client.Logger.Errorf(nil, "discarded %d packets to agent-gateway", i)
	}
}

// Pipe send packages to the agent-gateway with defined priorities and expiration rules
func (client *Client) Pipe(pack Package) {
	if client.pipe == nil {
		panic("client pipe not defined")
	}
	i := client.pipe.Send(pack)
	if i > 0 {
		client.Logger.Errorf(nil, "discarded %d packets to agent-gateway", i)
	}
}

// AddListener adds a listener for a specific packet kind
func (client *Client) AddListener(kind proto.PacketKind, listener func(in []byte) ([]byte, error)) {
	if err := client.channel.AddListener(kind.String(), listener); err != nil {
		panic(err)
	}
}

// InitClient inits client
func InitClient(
	args map[string]interface{},
	version string,
	startID string,
	accountID, clusterID uuid.UUID,
	secret []byte,
	parentLogger *log.Logger,
	connected chan bool,
) (*Client, error) {
	client := newClient(
		args["--gateway"].(string), version, startID, accountID, clusterID, secret,
		timeouts{
			protoHandshake: utils.MustParseDuration(args, "--timeout-proto-handshake"),
			protoWrite:     utils.MustParseDuration(args, "--timeout-proto-write"),
			protoRead:      utils.MustParseDuration(args, "--timeout-proto-read"),
			protoReconnect: utils.MustParseDuration(args, "--timeout-proto-reconnect"),
			protoBackoff:   utils.MustParseDuration(args, "--timeout-proto-backoff"),
		},
		parentLogger,
		!args["--no-send-logs"].(bool),
		args["--packets-v2"].(bool),
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

	err := client.Connect(connected)

	return client, err
}
