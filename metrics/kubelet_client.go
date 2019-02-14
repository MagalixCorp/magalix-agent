package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"golang.org/x/sync/errgroup"
	"io/ioutil"
	"k8s.io/client-go/rest"
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

type NodeGet func(node *kuber.Node, path_ string) ([]byte, error)

type KubeletClient struct {
	*log.Logger

	scanner *scanner.Scanner
	kube    *kuber.Kube

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
	nodeGet := func(node *kuber.Node, path string) ([]byte, error) {
		subResources := []string{"proxy"}
		subResources = append(subResources, strings.Split(path, "/")...)

		r, err := client.kube.Clientset.
			CoreV1().
			RESTClient().
			Get().
			Resource("nodes").
			Name(node.Name).
			SubResource(subResources...).
			DoRaw()

		return r, err
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
	nodeGet := func(node *kuber.Node, path_ string) ([]byte, error) {
		base := fmt.Sprintf("http://%s:%v", node.IP, client.httpPort)
		url_ := joinUrl(base, path_)

		ctx := karma.Describe("url", url_)

		restClient, ok := client.kube.Clientset.RESTClient().(*rest.RESTClient)
		if !ok {
			return nil, karma.Format(
				nil,
				"invalid cast, please contact developers",
			)
		}

		response, err := restClient.Client.Get(url_)
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

	b, err := nodeGet(node, "stats/summary")
	if err != nil {
		return ctx.Format(err, "node access test failed")
	}

	var response interface{}
	err = parseJSON(b, &response)
	if err != nil {
		return ctx.Format(err, "node access test failed")
	}
	return nil
}

func (client *KubeletClient) Get(
	node *kuber.Node,
	path string,
) ([]byte, error) {
	return client.nodeGet(node, path)
}

func (client *KubeletClient) GetJson(
	node *kuber.Node,
	path string,
	response interface{},
) error {
	b, err := client.Get(node, path)
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

	client := &KubeletClient{
		Logger: logger,

		scanner: scanner,
		kube:    kube,

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
