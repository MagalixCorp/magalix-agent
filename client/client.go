package client

import (
	"net/url"
	"sync"
	"time"

	"github.com/MagalixCorp/magalix-agent/v3/proto"
	"github.com/MagalixTechnologies/channel"
	"github.com/MagalixTechnologies/core/logger"
	"github.com/MagalixTechnologies/uuid-go"
)

const (
	ProtocolMajorVersion = 2
	ProtocolMinorVersion = 4
)

type timeouts struct {
	protoHandshake time.Duration
	protoWrite     time.Duration
	protoRead      time.Duration
	protoReconnect time.Duration
	protoBackoff   time.Duration
}

// Client agent gateway client
// Client agent gateway client
type Client struct {
	address          string
	version          string
	startID          string
	AccountID        uuid.UUID
	ClusterID        uuid.UUID
	secret           []byte
	ServerVersion    string
	AgentPermissions string

	channel    *channel.Client
	connected  bool
	authorized bool

	shouldSendLogs bool
	logBuffer      proto.PacketLogs

	// for thread blocked on connection
	blocked  sync.Map
	blockedM sync.Mutex

	timeouts timeouts

	lastSent time.Time

	pipe       *Pipe
	pipeStatus *Pipe

	watchdogTicker *time.Ticker
}

// newClient creates a new client
func newClient(
	address string,
	version string,
	startID string,
	accountID uuid.UUID,
	clusterID uuid.UUID,
	secret []byte,
	serverVersion string,
	agentPermissions string,
	timeouts timeouts,
	shouldSendLogs bool,
) *Client {
	gwUrl, err := url.Parse(address)
	if err != nil {
		panic(err)
	}

	address = gwUrl.String()

	client := &Client{
		address:          address,
		version:          version,
		startID:          startID,
		AccountID:        accountID,
		ClusterID:        clusterID,
		secret:           secret,
		ServerVersion:    serverVersion,
		shouldSendLogs:   shouldSendLogs,
		AgentPermissions: agentPermissions,
		channel: channel.NewClient(*gwUrl, channel.ChannelOptions{
			ProtoHandshake: timeouts.protoHandshake,
			ProtoWrite:     timeouts.protoWrite,
			ProtoRead:      timeouts.protoRead,
			ProtoReconnect: timeouts.protoReconnect,
		}),
		logBuffer: make(proto.PacketLogs, 0, 10),
		blocked:   sync.Map{},
		blockedM:  sync.Mutex{},

		timeouts: timeouts,
	}

	client.pipe = NewPipe(client)
	client.pipeStatus = NewPipe(client)

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

		if try+1 < limit {
			time.Sleep(timeout)
		}
	}
	if err != nil {
		logger.Errorw("unhandled error occurred, retries limit is exceeded", "limit", limit, "error", err)
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

	req, err = proto.EncodeSnappy(in)
	if err != nil {
		return err
	}

	res, err := client.channel.Send(kind.String(), req)
	if err != nil {
		return err
	}

	client.blockedM.Lock()
	client.lastSent = time.Now()
	client.blockedM.Unlock()

	if out == nil {
		return nil
	}

	return proto.DecodeSnappy(res, out)
}

// Send sends a packet to the agent-gateway if there is an established connection it internally uses client.send
func (client *Client) Send(kind proto.PacketKind, in interface{}, out interface{}) error {
	logger.Debugw("sending package", "kind", kind)

	defer logger.Debugw("package sent", "kind", kind)
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
		logger.Errorw("discarded packets to agent-gateway", "#packets", i)
	}
}

// Pipe send packages to the agent-gateway with defined priorities and expiration rules
func (client *Client) Pipe(pack Package) {
	if client.pipe == nil {
		panic("client pipe not defined")
	}
	client.pipe.Send(pack)
	// i := client.pipe.Send(pack)  Uncomment after piping logs logic is implemented/revisited
	// if i > 0 {
	// 	logger.Errorw("discarded packets to agent-gateway", "#packets", i)
	// }
}

// AddListener adds a listener for a specific packet kind
func (client *Client) AddListener(kind proto.PacketKind, listener func(in []byte) ([]byte, error)) {
	if err := client.channel.AddListener(kind.String(), listener); err != nil {
		panic(err)
	}
}

// InitClient inits client
func InitClient(
	version string,
	startID string,
	accountID, clusterID uuid.UUID,
	secret []byte,
	serverVersion string,
	agentPermissions string,
	gatewayUrl string,
	protoHandshake time.Duration,
	protoWrite time.Duration,
	protoRead time.Duration,
	protoReconnect time.Duration,
	protoBackoff time.Duration,
	sendLogs bool,
) *Client {
	client := newClient(
		gatewayUrl,
		version,
		startID,
		accountID,
		clusterID,
		secret,
		serverVersion,
		agentPermissions,
		timeouts{
			protoHandshake: protoHandshake,
			protoWrite:     protoWrite,
			protoRead:      protoRead,
			protoReconnect: protoReconnect,
			protoBackoff:   protoBackoff,
		},
		sendLogs,
	)
	return client
}
