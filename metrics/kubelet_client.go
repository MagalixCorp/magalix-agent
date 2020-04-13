package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/MagalixCorp/magalix-agent/v2/kuber"
	"github.com/MagalixCorp/magalix-agent/v2/utils"
	"github.com/MagalixTechnologies/log-go"
	"github.com/reconquest/karma-go"
	"golang.org/x/sync/errgroup"
)

const noPortHelp = `
Can't find a working kubelet address.
-----------------------------------------

Please verify that one of the following is correct:

1. the agent ClusterRole has the apiGroup ["metrics.k8s.io"] and its resources has ["nodes", "nodes/stats", "nodes/metrics", "nodes/proxy"]
See this for more info https://kubernetes.io/docs/reference/command-line-tools-reference/kubelet-authentication-authorization/#kubelet-authorization
You can just rerun the connect cluster command you got from Magalix console to apply those rules. If this doesn't help please contact Magalix support.

2. the cluster has the http readonly port enabled and set to the default 10255 or the custom port is passed correctly to the agent container as argument '--kubelet-port=<your-port>'
Note that http port is deprecated in k8s v11 and above, so please make sure to use the api-server method above for best compatibility.
`

func joinUrl(address, path string) string {
	u, _ := url.Parse(address)
	u.Path = path
	return u.String()
}

func readResponseBytes(
	resp *http.Response, logger *log.Logger,
) ([]byte, error) {
	if resp.Body != nil {
		defer func() {
			if err := resp.Body.Close(); err != nil {
				logger.Errorf(err, "error while closing body")
			}
		}()
	}
	return ioutil.ReadAll(resp.Body)
}

func parseJSON(b []byte, response interface{}) (err error) {
	err = json.Unmarshal(b, &response)
	if err != nil {
		return karma.
			Format(
				err,
				"unable to unmarshal response: %s",
				utils.TruncateString(string(b), 100),
			)
	}
	return nil
}

func parseJSONStream(body io.Reader, response interface{}) (err error) {
	err = json.NewDecoder(body).Decode(&response)
	if err != nil {
		return karma.
			Format(
				err,
				"unable to unmarshal response to json",
			)
	}
	return nil
}

type NodePathGetter func(node *corev1.Node, path_ string) string

type KubeletClient struct {
	*log.Logger

	nodesProvider NodesProvider

	kube       *kuber.Kube
	restClient *rest.RESTClient

	httpPort string

	getNodeUrl NodePathGetter
}

func (client *KubeletClient) init() (err error) {
	nodeGet, err := client.discoverNodesAddress()

	if err != nil {
		print(noPortHelp)
		return karma.Format(
			err,
			"unable to get access to kubelet apis.",
		)
	}

	client.getNodeUrl = nodeGet

	return nil
}

func (client *KubeletClient) discoverNodesAddress() (
	nodeGet NodePathGetter,
	err error,
) {

	nodes, err := client.nodesProvider.GetNodes()
	if err != nil {
		return nil, karma.Format(err, "can't test kubelet access")
	}
	if len(nodes) == 0 {
		return nil,
			karma.Format(
				nil,
				"can't test kubelet access. no discovered nodes",
			)
	}

	ctx := context.TODO()
	group, ctx := errgroup.WithContext(ctx)
	once := sync.Once{}
	found := make(chan struct{}, 0)
	done := make(chan struct{}, 0)

	setResult := func(fn NodePathGetter, isApiServer *bool) {
		if isApiServer != nil {
			if *isApiServer {
				client.Info(
					"using api-server node proxy to access kubelet metrics",
				)
			} else {
				client.Infof(
					karma.
						Describe("port", client.httpPort),
					"using direct kubelet api through http port",
				)
			}
			nodeGet = fn
		}
		close(found)
	}

	processNode := func(n corev1.Node) {
		group.Go(func() error {
			getAddr, isApiServer, err := client.discoverNodeAddress(&n)
			if err == nil {
				once.Do(func() {
					setResult(getAddr, isApiServer)
				})
			}
			return err
		})

	}

	for _, node := range nodes {
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
	node *corev1.Node,
) (nodeGet NodePathGetter, isApiServer *bool, err error) {
	isApiServer = new(bool)

	ctx := karma.
		Describe("node", node.Name).
		Describe("ip", GetNodeIP(node))

	*isApiServer = true
	nodeGet, err = client.tryApiServerProxy(ctx, node)
	if err == nil {
		return
	}

	*isApiServer = false
	nodeGet, err = client.tryDirectAccess(ctx, node)
	if err == nil {
		return
	}

	isApiServer = nil

	return
}

func (client *KubeletClient) tryApiServerProxy(
	ctx *karma.Context,
	node *corev1.Node,
) (NodePathGetter, error) {
	getNodeUrl := func(node *corev1.Node, path string) string {
		subResources := []string{"proxy"}
		subResources = append(subResources, strings.Split(path, "/")...)

		return client.kube.Clientset.
			CoreV1().
			RESTClient().
			Get().
			Resource("nodes").
			Name(node.Name).
			SubResource(subResources...).
			URL().
			String()
	}
	err := client.testNodeAccess(ctx, node, getNodeUrl)
	if err != nil {
		// can't use api-server proxy
		client.Warning(
			ctx.
				Format(
					err,
					"can't use api-server proxy to kubelet apis.",
				),
		)
		return nil, err
	}
	return getNodeUrl, nil
}

func (client *KubeletClient) tryDirectAccess(
	ctx *karma.Context,
	node *corev1.Node,
) (NodePathGetter, error) {
	getNodeUrl := func(node *corev1.Node, path_ string) string {
		base := fmt.Sprintf("http://%s:%v", GetNodeIP(node), client.httpPort)
		return joinUrl(base, path_)
	}
	err := client.testNodeAccess(ctx, node, getNodeUrl)
	if err != nil {
		client.Warning(
			ctx.
				Describe("port", client.httpPort).
				Format(
					err,
					"can't use direct kubelet http port.",
				),
		)
		return nil, err
	}
	return getNodeUrl, nil
}

func (client *KubeletClient) testNodeAccess(
	ctx *karma.Context, node *corev1.Node, getNodeUrl NodePathGetter,
) error {
	ctx = ctx.
		Describe("path", "stats/summary")

	url_ := getNodeUrl(node, "stats/summary")
	resp, err := client.get(url_)
	if err != nil {
		return ctx.Format(err, "node access test failed")
	}

	b, err := readResponseBytes(resp, client.Logger)

	var response interface{}
	err = parseJSON(b, &response)
	if err != nil {
		return ctx.Format(err, "node access test failed")
	}
	return nil
}

func (client *KubeletClient) get(url_ string) (*http.Response, error) {
	ctx := karma.Describe("url", url_)
	resp, err := client.restClient.Client.Get(url_)
	if err != nil {
		return nil, ctx.Reason(err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, ctx.Format(
			"GET request returned non OK status %s",
			resp.Status,
		)
	}
	return resp, nil
}

func (client *KubeletClient) Get(
	node *corev1.Node,
	path string,
) (*http.Response, error) {
	url_ := client.getNodeUrl(node, path)
	return client.get(url_)
}

func (client *KubeletClient) GetBytes(
	node *corev1.Node,
	path string,
) ([]byte, error) {
	resp, err := client.Get(node, path)
	if err != nil {
		return nil, err
	}

	return readResponseBytes(resp, client.Logger)
}

func (client *KubeletClient) GetJson(
	node *corev1.Node,
	path string,
	response interface{},
) error {
	resp, err := client.Get(node, path)
	if err != nil {
		return err
	}

	return parseJSONStream(resp.Body, &response)
}

type NodesProvider interface {
	GetNodes() ([]corev1.Node, error)
}

func NewKubeletClient(
	logger *log.Logger,
	nodesProvider NodesProvider,
	kube *kuber.Kube,
	args map[string]interface{},
) (*KubeletClient, error) {

	restClient, ok := kube.Clientset.RESTClient().(*rest.RESTClient)
	if !ok {
		return nil, karma.Format(
			nil,
			"invalid cast, please contact developers",
		)
	}

	client := &KubeletClient{
		Logger: logger,

		nodesProvider: nodesProvider,

		kube:       kube,
		restClient: restClient,

		httpPort: args["--kubelet-port"].(string),
	}

	err := client.init()
	if err != nil {
		return nil, err
	}

	return client, nil
}

func GetNodeIP(node *corev1.Node) string {
	var address string
	for _, addr := range node.Status.Addresses {
		if addr.Type == corev1.NodeInternalIP {
			address = addr.Address
		}
	}

	return address
}
