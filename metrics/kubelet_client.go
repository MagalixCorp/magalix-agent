package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"golang.org/x/sync/errgroup"
	"io"
	"io/ioutil"
	"k8s.io/client-go/rest"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/MagalixCorp/magalix-agent/kuber"
	"github.com/MagalixCorp/magalix-agent/scanner"
	"github.com/MagalixCorp/magalix-agent/utils"
	"github.com/MagalixTechnologies/log-go"
	"github.com/reconquest/karma-go"
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

type NodeGet func(node *kuber.Node, path_ string) (*http.Response, error)

type KubeletClient struct {
	*log.Logger

	scanner *scanner.Scanner

	kube       *kuber.Kube
	restClient *rest.RESTClient

	httpPort string

	nodeGet NodeGet
	//getNodeAddress func(node *kuber.Node) string
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

	client.nodeGet = nodeGet

	return nil
}

func (client *KubeletClient) discoverNodesAddress() (
	nodeGet NodeGet,
	err error,
) {

	nodes := client.scanner.GetNodes()
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

	setResult := func(fn NodeGet, isApiServer *bool) {
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

	processNode := func(n kuber.Node) {
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
) (nodeGet NodeGet, isApiServer *bool, err error) {
	isApiServer = new(bool)

	ctx := karma.
		Describe("node", node.Name).
		Describe("ip", node.IP)

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
	node *kuber.Node,
) (NodeGet, error) {
	nodeGet := func(node *kuber.Node, path string) (*http.Response, error) {
		subResources := []string{"proxy"}
		subResources = append(subResources, strings.Split(path, "/")...)

		url_ := client.kube.Clientset.
			CoreV1().
			RESTClient().
			Get().
			Resource("nodes").
			Name(node.Name).
			SubResource(subResources...).
			URL().
			String()

		return client.restClient.Client.Get(url_)
	}
	err := client.testNodeAccess(ctx, node, nodeGet)
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
	return nodeGet, nil
}

func (client *KubeletClient) tryDirectAccess(
	ctx *karma.Context,
	node *kuber.Node,
) (NodeGet, error) {
	nodeGet := func(node *kuber.Node, path_ string) (*http.Response, error) {
		base := fmt.Sprintf("http://%s:%v", node.IP, client.httpPort)
		url_ := joinUrl(base, path_)

		r, err := client.restClient.Client.Get(url_)
		if err != nil {
			return nil, karma.Describe("url", url_).Reason(err)
		}
		return r, nil
	}
	err := client.testNodeAccess(ctx, node, nodeGet)
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
	return nodeGet, nil
}

func (client *KubeletClient) testNodeAccess(
	ctx *karma.Context, node *kuber.Node, nodeGet NodeGet,
) error {
	ctx = ctx.
		Describe("path", "stats/summary")

	resp, err := nodeGet(node, "stats/summary")
	if err != nil {
		return ctx.Format(err, "node access test failed")
	}
	// TODO: check status code

	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return ctx.Format(
			err,
			"unable to read response body",
		)
	}
	_ = resp.Body.Close()

	var response interface{}
	err = parseJSON(b, &response)
	if err != nil {
		return ctx.Format(err, "node access test failed")
	}
	return nil
}

func (client *KubeletClient) GetBytes(
	node *kuber.Node,
	path string,
) ([]byte, error) {
	resp, err := client.Get(node, path)
	if err != nil {
		return nil, err
	}

	return client.readResponseBytes(resp)
}

func (client *KubeletClient) Get(
	node *kuber.Node,
	path string,
) (*http.Response, error) {
	resp, err := client.nodeGet(node, path)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, karma.Format(
			"GET request for URL %q returned HTTP status %s",
			resp.Request.URL.String(),
			resp.Status,
		)
	}

	return resp, nil
}

func (client *KubeletClient) GetJson(
	node *kuber.Node,
	path string,
	response interface{},
) error {
	b, err := client.GetBytes(node, path)
	if err != nil {
		return err
	}

	return parseJSON(b, &response)
}

func NewKubeletClient(
	logger *log.Logger,
	scanner *scanner.Scanner,
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

		scanner:    scanner,
		restClient: restClient,

		httpPort: args["--kubelet-port"].(string),
	}

	err := client.init()
	if err != nil {
		return nil, err
	}

	return client, nil
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

func parseJSONReader(body io.Reader, response interface{}) (err error) {
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

func (client *KubeletClient) readResponseBytes(
	resp *http.Response,
) ([]byte, error) {
	if resp.Body != nil {
		defer func() {
			err := resp.Body.Close()
			client.Errorf(err, "error while closing body")
		}()
	}
	return ioutil.ReadAll(resp.Body)
}
