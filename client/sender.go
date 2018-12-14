package client

import (
	"errors"
	"time"

	"github.com/MagalixCorp/magalix-agent/proto"
	"github.com/MagalixTechnologies/channel"
	"github.com/reconquest/karma-go"
)

// hello Sends hello package
func (client *Client) hello() error {
	var hello proto.PacketHello
	err := client.Send(proto.PacketKindHello, proto.PacketHello{
		Major: ProtocolMajorVersion,
		Minor: ProtocolMinorVersion,
	}, &hello)
	if err != nil {
		return err
	}

	client.Infof(
		karma.
			Describe("client/protocol/major", ProtocolMajorVersion).
			Describe("client/protocol/minor", ProtocolMinorVersion).
			Describe("server/protocol/major", hello.Major).
			Describe("server/protocol/minor", hello.Minor),
		"hello phase has been finished",
	)

	return nil
}

// authorize authorizes the client
func (client *Client) authorize() error {
	var question proto.PacketAuthorizationQuestion
	err := client.Send(proto.PacketKindAuthorizationRequest, proto.PacketAuthorizationRequest{
		AccountID: client.AccountID,
		ClusterID: client.ClusterID,
	}, &question)
	if err != nil {
		return err
	}

	if len(question.Token) < 1024 {
		return karma.
			Describe("token_length", len(question.Token)).
			Describe("token", string(question.Token)).
			Format(
				err,
				"server asks authorization/answer with unsecured token",
			)
	}

	token, err := client.getAuthorizationToken(question.Token)
	if err != nil {
		return err
	}

	var success proto.PacketAuthorizationSuccess
	err = client.Send(proto.PacketKindAuthorizationAnswer, proto.PacketAuthorizationAnswer{
		Token: token,
	}, &success)
	if err != nil {
		if e, ok := err.(*channel.ProtocolError); ok {
			if e.Code == channel.InternalErrorCode {
				return errors.New("authorization has failed")
			}
		}
		return err
	}

	client.Infof(
		nil,
		"client %s has been authorized in cluster %s",
		client.AccountID,
		client.ClusterID,
	)
	client.Infof(nil, "authorization stage has been finished")

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

	context := karma.
		Describe("latency/client-server", pong.Started.Sub(started).String()).
		Describe("latency/server-client", now.Sub(pong.Started).String())

	client.Infof(context, "ping-pong has been finished")

	return nil
}

// sendBye sends bye to indicate exit
func (client *Client) sendBye(reason string) error {
	var response proto.PacketBye
	return client.Send(proto.PacketKindBye, proto.PacketBye{
		Reason: reason,
	}, &response)
}
