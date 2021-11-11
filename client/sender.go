package client

import (
	"time"

	"github.com/MagalixCorp/magalix-agent/v3/proto"
	"github.com/MagalixTechnologies/channel"
	"github.com/MagalixTechnologies/core/logger"
	"github.com/pkg/errors"
)

// hello Sends hello package
func (client *Client) hello() error {
	var hello proto.PacketHello
	err := client.send(proto.PacketKindHello, proto.PacketHello{
		Major:            ProtocolMajorVersion,
		Minor:            ProtocolMinorVersion,
		Build:            client.version,
		StartID:          client.startID,
		AccountID:        client.AccountID,
		ClusterID:        client.ClusterID,
		PacketV2Enabled:  true,
		ServerVersion:    client.ServerVersion,
		AgentPermissions: client.AgentPermissions,
		ClusterProvider:  client.ClusterProvider,
	}, &hello)
	if err != nil {
		return err
	}

	logger.Infow("hello phase has been finished",
		"client/protocol/major", ProtocolMajorVersion,
		"client/protocol/minor", ProtocolMinorVersion,
		"server/protocol/major", hello.Major,
		"server/protocol/minor", hello.Minor,
	)

	return nil
}

// authorize authorizes the client
func (client *Client) authorize() error {
	var question proto.PacketAuthorizationQuestion
	err := client.send(proto.PacketKindAuthorizationRequest, proto.PacketAuthorizationRequest{
		AccountID: client.AccountID,
		ClusterID: client.ClusterID,
	}, &question)
	if err != nil {
		return err
	}

	if len(question.Token) < 1024 {
		return errors.Wrapf(
			err,
			"server asks authorization/answer with unsecured token; token length: %d, token: %s",
			len(question.Token),
			string(question.Token),
		)
	}

	token, err := client.getAuthorizationToken(question.Token)
	if err != nil {
		return err
	}

	var success proto.PacketAuthorizationSuccess
	err = client.send(proto.PacketKindAuthorizationAnswer, proto.PacketAuthorizationAnswer{
		Token: token,
	}, &success)
	if err != nil {
		if e, ok := err.(*channel.ProtocolError); ok {
			if e.Code == channel.InternalErrorCode {
				return e
			}
		}
		return err
	}

	logger.Infow(
		"client is authorized",
		"accountID", client.AccountID,
		"clusterID", client.ClusterID,
	)

	return nil
}

// ping pings the client
func (client *Client) ping() error {
	started := time.Now().UTC()

	var pong proto.PacketPong
	err := client.Send(proto.PacketKindPing, proto.PacketPing{
		Started: started,
	}, &pong)
	if err != nil {
		return err
	}

	now := time.Now().UTC()

	logger.Debugw(
		"ping gateway has been finished",
		"latency/client-server", pong.Started.Sub(started).String(),
		"latency/server-client", now.Sub(pong.Started).String(),
	)

	return nil
}
