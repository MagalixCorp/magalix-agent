package metrics

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"github.com/MagalixCorp/magalix-agent/kuber"
	"github.com/MagalixCorp/magalix-agent/scanner"
	"github.com/MagalixCorp/magalix-agent/utils"
	"github.com/MagalixTechnologies/log-go"
	"github.com/reconquest/karma-go"
	"golang.org/x/sync/errgroup"
	"io/ioutil"
	"k8s.io/client-go/util/cert"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

const noPortHelp = `
Can't find a working kubelet address.
-----------------------------------------

Please verify that one of the following is correct:

1. the agent has the sufficient permissions to access stats and metrics resources.
Also verify that you are passing the correct --kubelet-insecure and --kubelet-root-ca-cert arguments to the agent.
See this for more info https://kubernetes.io/docs/reference/command-line-tools-reference/kubelet-authentication-authorization/

2. the cluster has the http readonly port enabled and set to the default 10255

3. the cluster has http readonly port enabled and passed correctly to the agent container as argument '--kubelet-port=<your-port>'
`

func joinUrl(address, path string) string {
	u, _ := url.Parse(address)
	u.Path = path
	return u.String()
}

type NodeAddressGetter func(node *kuber.Node) string

type KubeletClient struct {
	*log.Logger
	*http.Client

	httpPort          string
	singleKubeletHost string
	token             string

	schema string
	port   *string

	scanner *scanner.Scanner

	getNodeAddress func(node *kuber.Node) string
}

func (client *KubeletClient) init() (err error) {
	getNodeAddress, schema, port, err := client.discoverNodesAddress()

	if err != nil {
		print(noPortHelp)
		return karma.Format(
			nil,
			"unable to get working kubelet address.",
		)
	}

	client.schema = schema
	client.port = port
	client.getNodeAddress = getNodeAddress

	return nil
}

func (client *KubeletClient) discoverNodesAddress() (
	getNodeKubeletAddress NodeAddressGetter,
	schema string,
	port *string,
	err error,
) {

	if client.singleKubeletHost != "" {
		getNodeKubeletAddress = func(node *kuber.Node) string {
			return client.singleKubeletHost
		}

		err = client.testNodeAccess(nil, getNodeKubeletAddress)

		if err == nil {
			client.Infof(
				karma.Describe("address", client.singleKubeletHost),
				"using single host kubelet",
			)
		}

		return
	}

	nodes := client.scanner.GetNodes()
	if len(nodes) == 0 {
		return nil,
			"",
			nil,
			karma.Format(
				nil,
				"can't test kubelet ports. no discovered nodes",
			)
	}

	ctx := context.TODO()
	group, ctx := errgroup.WithContext(ctx)
	once := sync.Once{}
	found := make(chan struct{}, 0)
	done := make(chan struct{}, 0)

	setResult := func(s, p *string) {
		schema = *s
		port = p

		if p == nil {
			client.Infof(
				karma.
					Describe("port", "<auto>").
					Describe("schema", *s),
				"using auto port discovery",
			)
			getNodeKubeletAddress = func(node *kuber.Node) string {
				return fmt.Sprintf(
					"%s://%s:%v",
					*s,
					node.IP,
					node.KubeletPort,
				)
			}
		} else {
			client.Infof(
				karma.
					Describe("port", *p).
					Describe("schema", *s),
				"using specified kubelet port",
			)
			getNodeKubeletAddress = func(node *kuber.Node) string {
				return fmt.Sprintf(
					"%s://%s:%v",
					*s,
					node.IP,
					*port,
				)
			}

		}
		close(found)
	}

	processNode := func(n kuber.Node) {
		group.Go(func() error {
			s, p, err := client.discoverNodeAddress(&n)
			if err == nil {
				once.Do(func() {
					setResult(s, p)
				})
			}
			return err
		})

	}

	for _, node := range client.scanner.GetNodes() {
		processNode(node)
	}

	go func() {
		err = group.Wait()
		close(done)
	}()

	select {
	case <-found:
	case <-done:
		break
	}

	return
}

func (client *KubeletClient) discoverNodeAddress(
	node *kuber.Node,
) (schema, port *string, err error) {

	getNodeKubeletAddress := func(node *kuber.Node) string {
		p := fmt.Sprintf("%d", node.KubeletPort)
		if port != nil {
			p = *port
		}
		return fmt.Sprintf("%s://%s:%v", *schema, node.IP, p)
	}

	ctx := karma.
		Describe("node", node.Name).
		Describe("ip", node.IP)

	schema = new(string)
	*schema = "https"
	err = client.testNodeAccess(node, getNodeKubeletAddress)
	if err != nil {
		//	can't use TLS port for some reason
		client.Warning(
			ctx.
				Describe("schema", *schema).
				Describe("port", "<auto>").
				Format(
					err,
					"can't use kubelet TLS port. "+
						"falling back to http readonly port: %s",
					client.httpPort,
				),
		)
		port = new(string)
		*schema = "http"
		*port = client.httpPort

		err = client.testNodeAccess(node, getNodeKubeletAddress)
		if err != nil {
			client.Warning(
				ctx.
					Describe("schema", *schema).
					Describe("port", *port).
					Format(
						err,
						"can't use kubelet http readonly port %s",
						port,
					),
			)
			schema = nil
			port = nil
		}
	}

	return
}

func (client *KubeletClient) testNodeAccess(
	node *kuber.Node, getAddr NodeAddressGetter,
) error {
	testAddress := getAddr(node)
	testUrl := joinUrl(testAddress, "/stats/summary")

	var response interface{}
	err := client.getJson(testUrl, &response)
	if err != nil {
		return err
	}
	return nil
}

func (client *KubeletClient) getNodeUrl(node *kuber.Node, path string) string {
	host := client.getNodeAddress(node)
	return joinUrl(host, path)
}

func (client *KubeletClient) get(url string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	// add authorization header to the req (if any)
	if client.token != "" {
		req.Header.Add("Authorization", "Bearer "+client.token)
	}

	ctx := karma.Describe("url", url)
	response, err := client.Client.Do(req)
	if err != nil {
		return nil, ctx.Reason(err)
	}

	b, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return nil, ctx.Format(
			err,
			"unable to read response body",
		)
	}
	_ = response.Body.Close()

	return b, nil
}

func (client *KubeletClient) getJson(url string, response interface{}) error {
	b, err := client.get(url)
	if err != nil {
		return err
	}

	err = json.Unmarshal(b, &response)
	if err != nil {
		return karma.Describe("url", url).
			Format(
				err,
				"unable to unmarshal response: %s",
				utils.TruncateString(string(b), 100),
			)
	}
	return nil
}

func (client *KubeletClient) Get(
	node *kuber.Node,
	path string,
) ([]byte, error) {
	u := client.getNodeUrl(node, path)
	return client.get(u)
}

func (client *KubeletClient) GetJson(
	node *kuber.Node,
	path string,
	response interface{},
) error {
	u := client.getNodeUrl(node, path)
	return client.getJson(u, &response)
}

func NewKubeletClient(
	logger *log.Logger,
	scanner *scanner.Scanner,
	args map[string]interface{},
) (*KubeletClient, error) {

	kubeletAddress, _ := args["--kubelet-address"].(string)
	kubeletPort, _ := args["--kubelet-port"].(string)
	kubeletToken, _ := args["--kubelet-token"].(string)

	insecure := args["--kubelet-insecure"].(bool)

	if kubeletAddress != "" && strings.HasPrefix(kubeletAddress, "/") {
		kubeletUrl, err := url.Parse(kubeletAddress)
		if kubeletUrl.Scheme == "" {
			err = karma.Format(
				nil,
				"kubelet address don't have schema",
			)
		}
		if err != nil {
			return nil, karma.Format(
				nil,
				"invalid kubelet address %s.",
				kubeletAddress,
			)
		}
	}

	tlsConfig := &tls.Config{
		InsecureSkipVerify: insecure,
	}

	if rootCAFile, ok := args["--kubelet-root-ca-cert"].(string); ok {
		if certPool, err := cert.NewPool(rootCAFile); err != nil {
			logger.Errorf(err, "expected to load root CA config from %s, but got err: %v", rootCAFile)
		} else {
			tlsConfig.RootCAs = certPool
		}
	}

	client := &KubeletClient{
		Logger: logger,
		Client: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: tlsConfig,
			},
		},

		httpPort:          kubeletPort,
		singleKubeletHost: kubeletAddress,
		token:             kubeletToken,

		scanner: scanner,
	}

	err := client.init()
	if err != nil {
		return nil, karma.Format(
			err,
			"unable to init kubelet client",
		)
	}

	return client, nil
}
