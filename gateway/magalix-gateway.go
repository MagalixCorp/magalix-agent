package gateway

import (
	"context"
	"github.com/MagalixCorp/magalix-agent/v2/agent"
	"github.com/MagalixCorp/magalix-agent/v2/client"
	"github.com/MagalixTechnologies/core/logger"
	"github.com/MagalixTechnologies/uuid-go"
	"go.uber.org/zap/zapcore"
	"time"
)

type MagalixGateway struct {
	MgxAgentGatewayUrl string

	AccountID    uuid.UUID
	ClusterID    uuid.UUID
	ClientSecret []byte

	AgentVersion string
	AgentID      string

	K8sServerVersion string
	AgentPermissions string

	ProtoHandshake     time.Duration
	ProtoWriteTime     time.Duration
	ProtoReadTime      time.Duration
	ProtoReconnectTime time.Duration
	ProtoBackoff       time.Duration

	ShouldSendLogs bool

	gwClient         *client.Client
	connectedChan    chan bool
	cancelWorkers    context.CancelFunc
	submitAutomation agent.AutomationHandler
	triggerRestart   agent.RestartHandler
	changeLogLevel   agent.ChangeLogLevelHandler
}

func New(
	gatewayUrl string,
	accountID uuid.UUID,
	clusterID uuid.UUID,
	secret []byte,
	agentVersion string,
	agentID string,
	k8sServerVersion string,
	agentPermissions string,
	protoHandshake time.Duration,
	protoWriteTime time.Duration,
	protoReadTime time.Duration,
	protoReconnectTime time.Duration,
	protoBackoff time.Duration,
	sendLogs bool,
) *MagalixGateway {
	connected := make(chan bool)
	return &MagalixGateway{
		MgxAgentGatewayUrl: gatewayUrl,
		AccountID:          accountID,
		ClusterID:          clusterID,
		ClientSecret:       secret,
		AgentVersion:       agentVersion,
		AgentID:            agentID,
		K8sServerVersion:   k8sServerVersion,
		AgentPermissions:   agentPermissions,
		ProtoHandshake:     protoHandshake,
		ProtoWriteTime:     protoWriteTime,
		ProtoReadTime:      protoReadTime,
		ProtoReconnectTime: protoReconnectTime,
		ProtoBackoff:       protoBackoff,
		ShouldSendLogs:     sendLogs,
		connectedChan:      connected,
		gwClient: client.InitClient(
			agentVersion,
			agentID,
			accountID,
			clusterID,
			secret,
			k8sServerVersion,
			agentPermissions,
			gatewayUrl,
			protoHandshake,
			protoWriteTime,
			protoReadTime,
			protoReconnectTime,
			protoBackoff,
			sendLogs,
		),
	}
}

func (g *MagalixGateway) Start(ctx context.Context) error {
	if g.cancelWorkers != nil {
		g.cancelWorkers()
	}

	cancelCtx, cancel := context.WithCancel(ctx)
	g.cancelWorkers = cancel
	defer g.gwClient.Recover()

	return g.gwClient.Connect(cancelCtx, g.connectedChan)
}

func (g *MagalixGateway) Stop() error {
	g.cancelWorkers()
	return nil
}

func (g *MagalixGateway) WaitAuthorization() {
	logger.Info("waiting for connection and authorization")
	if g.gwClient.IsReady() {
		return
	}
	// Intentionally used without a timeout so it blocks indefinitely when not authorized
	<-g.connectedChan
	logger.Info("Connected and authorized")
}

func (g *MagalixGateway) GetLogsWriteSyncer() zapcore.WriteSyncer {
	return g.gwClient
}
