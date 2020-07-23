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
	"github.com/MagalixCorp/magalix-agent/v2/events"
	"github.com/MagalixCorp/magalix-agent/v2/executor"
	"github.com/MagalixCorp/magalix-agent/v2/kuber"
	"github.com/MagalixCorp/magalix-agent/v2/metrics"
	"github.com/MagalixCorp/magalix-agent/v2/proto"
	"github.com/MagalixCorp/magalix-agent/v2/scanner"
	"github.com/MagalixCorp/magalix-agent/v2/utils"
	"github.com/MagalixTechnologies/log-go"
	"github.com/MagalixTechnologies/uuid-go"
	"github.com/docopt/docopt-go"
	"github.com/reconquest/karma-go"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	_ "net/http/pprof"
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
                                              [default: 20s]
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
  --events-buffer-flush-interval <duration>  Events batch writer flush interval.
                                              [default: 10s]
  --events-buffer-size <size>                Events batch writer buffer size.
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
  --opt-in-analysis-data                     Send anonymous data for analysis.
  --analysis-data-interval <duration>        Analysis data send interval.
                                              [default: 5m]
  --packets-v2                               Enable v2 packets (without ids). This is deprecated and kept for backward compatability.
  --disable-metrics                          Disable metrics collecting and sending.
  --disable-events                           Disable events collecting and sending.
  --disable-scalar                           Disable in-agent scalar.
  --port <port>                              Port to start the server on for liveness and readiness probes
                                               [default: 80]
  --dry-run                                  Disable decision execution.
  --no-send-logs                             Disable sending logs to the backend.
  --debug                                    Enable debug messages.
  --trace                                    Enable debug and trace messages.
  --trace-log <path>                         Write log messages to specified file
                                              [default: trace.log]
  -h --help                                  Show this help.
  --version                                  Show version.
`

var version = "[manual build]"

var startID string

const (
	entitiesSyncTimeout = time.Minute
)

func getVersion() string {
	return strings.Join([]string{
		"magalix agent " + version,
		"protocol/major: " + fmt.Sprint(client.ProtocolMajorVersion),
		"protocol/minor: " + fmt.Sprint(client.ProtocolMinorVersion),
	}, "\n")
}

func main() {
	startID = uuid.NewV4().String()
	args, err := docopt.ParseArgs(usage, nil, getVersion())
	if err != nil {
		panic(err)
	}

	logger := log.New(
		args["--debug"].(bool),
		args["--trace"].(bool),
		args["--trace-log"].(string),
	)
	// we need to disable default exit 1 for FATAL messages because we also
	// need to send fatal messages on the remote server and send bye packet
	// after fatal message (if we can), therefore all exits will be controlled
	// manually
	logger.SetExiter(func(int) {})
	utils.SetLogger(logger)

	logger.Infof(
		karma.Describe("version", version).
			Describe("args", fmt.Sprintf("%q", utils.GetSanitizedArgs())),
		"magalix agent started",
	)

	secret, err := base64.StdEncoding.DecodeString(
		utils.ExpandEnv(args, "--client-secret", false),
	)
	if err != nil {
		logger.Fatalf(
			err,
			"unable to decode base64 secret specified as --client-secret flag",
		)
		os.Exit(1)
	}
	// TODO: remove
	// a hack to set default timeout for all http requests
	http.DefaultClient = &http.Client{
		Timeout: 20 * time.Second,
	}

	var (
		accountID = utils.ExpandEnvUUID(args, "--account-id")
		clusterID = utils.ExpandEnvUUID(args, "--cluster-id")
	)

	port := args["--port"].(string)
	probes := NewProbesServer(":"+port, logger)
	go func() {
		err = probes.Start()
		if err != nil {
			logger.Fatalf(err, "unable to start probes server")
			os.Exit(1)
		}
	}()

	connected := make(chan bool)
	gwClient, err := client.InitClient(args, version, startID, accountID, clusterID, secret, logger, connected)

	defer gwClient.WaitExit()
	defer gwClient.Recover()

	if err != nil {
		logger.Fatalf(err, "unable to connect to gateway")
		os.Exit(1)
	}

	logger.Infof(nil, "Waiting for connection and authorization")
	<-connected
	logger.Infof(nil, "Connected and authorized")
	probes.Authorized = true
	initAgent(args, gwClient, logger, accountID, clusterID)
}

func initAgent(args docopt.Opts, gwClient *client.Client, logger *log.Logger, accountID uuid.UUID, clusterID uuid.UUID) {
	logger.Infof(nil, "Initializing Agent")
	var (
		metricsEnabled  = !args["--disable-metrics"].(bool)
		eventsEnabled   = !args["--disable-events"].(bool)
		//scalarEnabled   = !args["--disable-scalar"].(bool)
		executorWorkers = utils.MustParseInt(args, "--executor-workers")
		dryRun          = args["--dry-run"].(bool)

		skipNamespaces []string
	)

	if namespaces, ok := args["--skip-namespace"].([]string); ok {
		skipNamespaces = namespaces
	}

	kRestConfig, err := getKRestConfig(logger, args)

	dynamicClient, err := dynamic.NewForConfig(kRestConfig)
	parentsStore := kuber.NewParentsStore()
	observer := kuber.NewObserver(
		logger,
		dynamicClient,
		parentsStore,
		make(chan struct{}, 0),
		time.Minute*5,
	)
	t := entitiesSyncTimeout
	err = observer.WaitForCacheSync(&t)
	if err != nil {
		logger.Fatalf(err, "unable to start entities watcher")
	}

	kube, err := kuber.InitKubernetes(kRestConfig, gwClient)
	if err != nil {
		logger.Fatalf(err, "unable to initialize Kubernetes")
		os.Exit(1)
	}

	optInAnalysisData := args["--opt-in-analysis-data"].(bool)
	analysisDataInterval := utils.MustParseDuration(
		args,
		"--analysis-data-interval",
	)

	var entityScanner *scanner.Scanner

	ew := entities.NewEntitiesWatcher(logger, observer, gwClient)
	err = ew.Start()
	if err != nil {
		logger.Fatalf(err, "unable to start entities watcher")
	}

	/*if scalarEnabled {
		scalar2.InitScalars(logger, kube, observer, dryRun)
	}*/

	entityScanner = scanner.InitScanner(
		gwClient,
		scanner.NewKuberFromObserver(ew),
		skipNamespaces,
		accountID,
		clusterID,
		optInAnalysisData,
		analysisDataInterval,
	)

	e := executor.InitExecutor(
		gwClient,
		kube,
		entityScanner,
		executorWorkers,
		dryRun,
	)

	gwClient.AddListener(proto.PacketKindDecision, e.Listener)
	gwClient.AddListener(proto.PacketKindRestart, func(in []byte) (out []byte, err error) {
		var restart proto.PacketRestart
		if err = proto.DecodeSnappy(in, &restart); err != nil {
			return
		}
		defer gwClient.Done(restart.Status, true)
		return nil, nil
	})

	if eventsEnabled {
		events.InitEvents(
			gwClient,
			kube,
			skipNamespaces,
			entityScanner,
			args,
		)
	}

	if metricsEnabled {
		var nodesProvider metrics.NodesProvider
		var entitiesProvider metrics.EntitiesProvider

		nodesProvider = observer
		entitiesProvider = observer

		err := metrics.InitMetrics(
			gwClient,
			nodesProvider,
			entitiesProvider,
			kube,
			optInAnalysisData,
			args,
		)
		if err != nil {
			gwClient.Fatalf(err, "unable to initialize metrics sources")
			os.Exit(1)
		}
	}

	go func() {
		http.ListenAndServe(":6060", nil)
	}()
}

func getKRestConfig(
	logger *log.Logger,
	args map[string]interface{},
) (config *rest.Config, err error) {
	if args["--kube-incluster"].(bool) {
		logger.Infof(nil, "initializing kubernetes incluster config")

		config, err = rest.InClusterConfig()
		if err != nil {
			return nil, karma.Format(
				err,
				"unable to get incluster config",
			)
		}

	} else {
		logger.Infof(
			nil,
			"initializing kubernetes user-defined config",
		)

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
