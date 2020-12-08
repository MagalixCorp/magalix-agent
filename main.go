package main

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"k8s.io/client-go/util/cert"

	"github.com/MagalixCorp/magalix-agent/v2/client"
	"github.com/MagalixCorp/magalix-agent/v2/entities"
	"github.com/MagalixCorp/magalix-agent/v2/executor"
	"github.com/MagalixCorp/magalix-agent/v2/kuber"
	"github.com/MagalixCorp/magalix-agent/v2/metrics"
	"github.com/MagalixCorp/magalix-agent/v2/proto"
	"github.com/MagalixCorp/magalix-agent/v2/utils"
	"github.com/MagalixTechnologies/core/logger"
	"github.com/MagalixTechnologies/uuid-go"
	"github.com/docopt/docopt-go"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

var usage = `agent - magalix services agent.

Usage:
  agent -h | --help
  agent [options] (--kube-url= | --kube-incluster) [--skip-namespace=]... [--source=]...

Options:
  --gateway <address>                        Connect to specified Magalix Kubernetes Agent gateway.
                                              [default: wss://gateway.agent.magalix.cloud]
  --account-id <identifier>                  Your account ID in Magalix.
                                              [default: $ACCOUNT_ID]
  --cluster-id <identifier>                  Your cluster ID in Magalix.
                                              [default: $CLUSTER_ID]
  --client-secret <secret>                   Unique and secret client token.
                                              [default: $SECRET]
  --kube-url <url>                           Use specified URL and token for access to kubernetes
                                              cluster.
  --kube-insecure                            Insecure skip SSL verify.
  --kube-root-ca-cert <filepath>             Filepath to root CA cert.
  --kube-token <token>                        Use specified token for access to kubernetes cluster.
  --kube-incluster                           Automatically determine kubernetes clientset
                                              configuration. Works only if program is
                                              running inside kubernetes cluster.
  --kube-timeout <duration>                  Timeout of requests to kubernetes apis.
                                              [default: 30s]
  --skip-namespace <pattern>                 Skip namespace matching a pattern (e.g. system-*),
                                              can be specified multiple times.
  --source <source>                          Specify source for metrics instead of
                                              automatically detected.
                                              Supported sources are:
                                              * kubelet;
  --kubelet-port <port>                      Override kubelet port for
                                              automatically discovered nodes.
                                              [default: 10255]
  --kubelet-backoff-sleep <duration>         Timeout of backoff policy.
                                              Timeout will be multiplied from 1 to 10.
                                              [default: 300ms]
  --kubelet-backoff-max-retries <retries>    Max retries of backoff policy, then consider failed.
                                              [default: 5]
  --metrics-interval <duration>              Metrics request and send interval.
                                              [default: 1m]
  --events-buffer-flush-interval <duration>  Events batch writer flush interval(Deprecated).
                                              [default: 10s]
  --events-buffer-size <size>                Events batch writer buffer size(Deprecated).
                                              [default: 20]
  --executor-workers <number>                 Executor concurrent workers count
                                              [default: 5]
  --timeout-proto-handshake <duration>       Timeout to do a websocket handshake.
                                              [default: 10s]
  --timeout-proto-write <duration>           Timeout to write a message to websocket channel.
                                              [default: 60s]
  --timeout-proto-read <duration>            Timeout to read a message from websocket channel.
                                              [default: 60s]
  --timeout-proto-reconnect <duration>       Timeout between reconnecting retries.
                                              [default: 1s]
  --timeout-proto-backoff <duration>         Timeout of backoff policy.
                                              Timeout will be multiplied from 1 to 10.
                                              [default: 300ms]
  --opt-in-analysis-data                     Send anonymous data for analysis.(Deprecated)
  --analysis-data-interval <duration>        Analysis data send interval.(Deprecated)
                                              [default: 5m]
  --packets-v2                               Enable v2 packets (without ids). (Deprecated)
  --disable-metrics                          Disable metrics collecting and sending.
  --disable-events                           Disable events collecting and sending(Deprecated).
  --disable-scalar                           Disable in-agent scalar.
  --port <port>                              Port to start the server on for liveness and readiness probes
                                               [default: 8080]
  --dry-run                                  Disable automation execution.
  --no-send-logs                             Disable sending logs to the backend.
  --debug                                    Enable debug messages.
  --trace                                    Enable debug and trace messages.
  --trace-log <path>                         Write log messages to specified file
                                              [default: trace.log]
  --log-level <string>                       Log level
                                              [default: warn]
  -h --help                                  Show this help.
  --version                                  Show version.
`

var version = "[manual build]"

var startID string

func getVersion() string {
	return strings.Join([]string{
		"magalix agent " + version,
		"protocol/major: " + fmt.Sprint(client.ProtocolMajorVersion),
		"protocol/minor: " + fmt.Sprint(client.ProtocolMinorVersion),
	}, "\n")
}

// SetLogLevel sets log level for the agent
func SetLogLevel(c *client.Client, level string) bool {

	ok := true
	switch level {
	case "info":
		logger.ConfigWriterSync(logger.InfoLevel, c)
	case "debug":
		logger.ConfigWriterSync(logger.DebugLevel, c)
	case "warn":
		logger.ConfigWriterSync(logger.WarnLevel, c)
	case "error":
		logger.ConfigWriterSync(logger.ErrorLevel, c)
	default:
		ok = false
	}
	logger.WithGlobal("accountID", c.AccountID, "clusterID", c.ClusterID)
	return ok
}

func main() {
	startID = uuid.NewV4().String()
	args, err := docopt.ParseArgs(usage, nil, getVersion())
	if err != nil {
		panic(err)
	}

	var (
		accountID = utils.ExpandEnvUUID(args, "--account-id")
		clusterID = utils.ExpandEnvUUID(args, "--cluster-id")
	)

	logger.Infow(
		"magalix agent started.....",
		"version", version,
		"args", fmt.Sprintf("%q", utils.GetSanitizedArgs()),
	)

	secret, err := base64.StdEncoding.DecodeString(
		utils.ExpandEnv(args, "--client-secret", false),
	)
	if err != nil {
		logger.Fatalw(
			"unable to decode base64 secret specified as --client-secret flag",
			"error", err,
		)
		os.Exit(1)
	}
	// TODO: remove
	// a hack to set default timeout for all http requests
	http.DefaultClient = &http.Client{
		Timeout: 20 * time.Second,
	}

	port := args["--port"].(string)
	probes := NewProbesServer(":" + port)
	go func() {
		err = probes.Start()
		if err != nil {
			logger.Fatalw("unable to start probes server", "error", err)
			os.Exit(1)
		}
	}()

	kRestConfig, err := getKRestConfig(args)

	kube, err := kuber.InitKubernetes(kRestConfig)
	if err != nil {
		logger.Fatalw("unable to initialize Kubernetes", "error", err)
		os.Exit(1)
	}

	k8sServerVersion, err := kube.GetServerVersion()
	if err != nil {
		logger.Warnw("failed to discover server version", "error", err)
	}

	agentPermissions, err := kube.GetAgentPermissions()
	if err != nil {
		agentPermissions = err.Error()
		logger.Warnw("Failed to get agent permissions", "error", err)
	}

	connected := make(chan bool)
	gwClient, err := client.InitClient(
		args, version, startID, accountID, clusterID, secret, k8sServerVersion, agentPermissions, connected,
	)

	defer gwClient.WaitExit()
	defer gwClient.Recover()
	logger.Info("waiting for connection and authorization")
	<-connected
	logger.Info("Connected and authorized")
	go gwClient.Sync()

	if ok := SetLogLevel(gwClient, args["--log-level"].(string)); !ok {
		logger.Fatalw("unsupported log level", "level", args["--log-level"].(string))
	}
	defer logger.Sync()

	if err != nil {
		logger.Fatalw("unable to connect to gateway")
		os.Exit(1)
	}

	probes.Authorized = true
	initAgent(args, gwClient, kRestConfig, kube, accountID, clusterID)
}

func initAgent(
	args docopt.Opts,
	gwClient *client.Client,
	kRestConfig *rest.Config,
	kube *kuber.Kube,
	accountID uuid.UUID,
	clusterID uuid.UUID,
) {
	logger.Info("Initializing Agent")
	var (
		metricsEnabled = !args["--disable-metrics"].(bool)
		//scalarEnabled   = !args["--disable-scalar"].(bool)
		executorWorkers = utils.MustParseInt(args, "--executor-workers")
		dryRun          = args["--dry-run"].(bool)
	)

	dynamicClient, err := dynamic.NewForConfig(kRestConfig)
	parentsStore := kuber.NewParentsStore()
	observer := kuber.NewObserver(
		dynamicClient,
		parentsStore,
		make(chan struct{}, 0),
		time.Minute*5,
	)

	err = observer.WaitForCacheSync()
	if err != nil {
		logger.Fatalw("unable to start entities watcher", "error", err)
	}

	k8sMinorVersion, err := kube.GetServerMinorVersion()
	if err != nil {
		logger.Warnw("failed to discover server minor version", "error", err)
	}

	ew := entities.NewEntitiesWatcher(observer, gwClient, k8sMinorVersion)

	ew.Start()

	/*if scalarEnabled {
		scalar2.InitScalars(logger, kube, observer, dryRun)
	}*/

	e := executor.InitExecutor(
		gwClient,
		kube,
		observer,
		executorWorkers,
		dryRun,
	)

	gwClient.AddListener(proto.PacketKindAutomation, e.Listener)
	gwClient.AddListener(proto.PacketKindRestart, func(in []byte) (out []byte, err error) {
		var restart proto.PacketRestart
		if err = proto.DecodeSnappy(in, &restart); err != nil {
			return
		}
		go gwClient.Done(restart.Status, true)
		return nil, nil

	})

	gwClient.AddListener(proto.PacketKindLogLevel, func(in []byte) (out []byte, err error) {
		var logLevel proto.PacketLogLevel
		if err = proto.DecodeSnappy(in, &logLevel); err != nil {
			logger.Error("Failed to decode log level packet")
			return nil, err
		}
		if ok := SetLogLevel(gwClient, logLevel.Level); !ok {
			msg := fmt.Sprintf("Got an unsupported log level: %s", logLevel.Level)
			logger.Warnw(msg)
			return nil, errors.New(msg)
		}
		return nil, nil

	})

	if metricsEnabled {
		err := metrics.InitMetrics(
			gwClient,
			observer,
			observer,
			kube,
			args,
		)
		if err != nil {
			logger.Fatalw("unable to initialize metrics sources", "error", err)
			os.Exit(1)
		}
	}
}

func getKRestConfig(
	args map[string]interface{},
) (config *rest.Config, err error) {
	if args["--kube-incluster"].(bool) {
		logger.Info("initializing kubernetes incluster config")

		config, err = rest.InClusterConfig()
		if err != nil {
			return nil, errors.Wrap(err, "unable to get incluster config")
		}

	} else {
		logger.Info("initializing kubernetes user-defined config")

		token, _ := args["--kube-token"].(string)
		if token == "" {
			token = os.Getenv("KUBE_TOKEN")
		}

		config = &rest.Config{}
		config.ContentType = runtime.ContentTypeJSON
		config.APIPath = "/api"
		config.Host = args["--kube-url"].(string)
		config.BearerToken = token

		{
			tlsClientConfig := rest.TLSClientConfig{}
			rootCAFile, ok := args["--kube-root-ca-cert"].(string)
			if ok {
				if _, err := cert.NewPool(rootCAFile); err != nil {
					fmt.Printf("Expected to load root CA config from %s, but got err: %v", rootCAFile, err)
				} else {
					tlsClientConfig.CAFile = rootCAFile
				}
				config.TLSClientConfig = tlsClientConfig
			}
		}

		if args["--kube-insecure"].(bool) {
			config.Insecure = true
		}
	}

	config.Timeout = utils.MustParseDuration(args, "--kube-timeout")

	return
}
