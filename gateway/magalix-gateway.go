package gateway

import (
	"context"
	"errors"
	"time"

	"github.com/MagalixCorp/magalix-agent/v3/agent"
	"github.com/MagalixCorp/magalix-agent/v3/client"
	"github.com/MagalixTechnologies/core/logger"
	"github.com/MagalixTechnologies/uuid-go"
	"go.uber.org/zap/zapcore"
)

const auditResultsBatchSize = 1000

type MagalixGateway struct {
	MgxAgentGatewayUrl string

	AccountID    uuid.UUID
	ClusterID    uuid.UUID
	ClientSecret []byte

	AgentVersion string
	AgentID      string

	K8sServerVersion string
	AgentPermissions string
	ClusterProvider  string

	ProtoHandshake     time.Duration
	ProtoWriteTime     time.Duration
	ProtoReadTime      time.Duration
	ProtoReconnectTime time.Duration
	ProtoBackoff       time.Duration

	ShouldSendLogs bool

	gwClient           *client.Client
	connectedChan      chan bool
	cancelWorkers      context.CancelFunc
	addConstraints     agent.ConstraintsHandler
	handleAuditCommand agent.AuditCommandHandler
	triggerRestart     agent.RestartHandler
	changeLogLevel     agent.ChangeLogLevelHandler
	auditResultsBuffer []*agent.AuditResult
	auditResultChan    chan *agent.AuditResult
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
	clusterProvider string,
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
		ClusterProvider:    clusterProvider,
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
			clusterProvider,
			gatewayUrl,
			protoHandshake,
			protoWriteTime,
			protoReadTime,
			protoReconnectTime,
			protoBackoff,
			sendLogs,
		),
		auditResultsBuffer: make([]*agent.AuditResult, 0, auditResultsBatchSize),
		auditResultChan:    make(chan *agent.AuditResult, 50),
	}
}

func (g *MagalixGateway) Start(ctx context.Context) error {
	if g.cancelWorkers != nil {
		g.cancelWorkers()
	}

	cancelCtx, cancel := context.WithCancel(ctx)
	g.cancelWorkers = cancel
	defer g.gwClient.Recover()

	go g.SendAuditResultsWorker(cancelCtx)

	return g.gwClient.Connect(cancelCtx, g.connectedChan)
}

func (g *MagalixGateway) Stop() error {
	g.cancelWorkers()
	return nil
}

func (g *MagalixGateway) WaitAuthorization(timeout time.Duration) error {
	logger.Info("waiting for connection and authorization")
	if g.gwClient.IsReady() {
		return nil
	}

	select {
	case <-g.connectedChan:
		logger.Info("Connected and authorized")
		return nil
	case <-time.After(timeout):
		err := errors.New("authorization timeout")
		logger.Error(err)
		return err
	}
}

func (g *MagalixGateway) GetLogsWriteSyncer() zapcore.WriteSyncer {
	return g.gwClient
}
