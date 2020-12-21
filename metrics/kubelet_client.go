package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"

	"github.com/MagalixCorp/magalix-agent/v2/kuber"
	"github.com/MagalixCorp/magalix-agent/v2/utils"
	"github.com/MagalixTechnologies/core/logger"
	"github.com/pkg/errors"
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

func readResponseBytes(resp *http.Response) ([]byte, error) {
	if resp.Body != nil {
		defer func() {
			if err := resp.Body.Close(); err != nil {
				logger.Errorw("error while closing body", "error", err)
			}
		}()
	}
	return ioutil.ReadAll(resp.Body)
}

func parseJSON(b []byte, response interface{}) (err error) {
	err = json.Unmarshal(b, &response)
	if err != nil {
		return errors.Wrapf(
			err,
			"unable to unmarshal response: %s", utils.TruncateString(string(b), 100),
		)
	}
	return nil
}

func parseJSONStream(body io.Reader, response interface{}) (err error) {
	err = json.NewDecoder(body).Decode(&response)
	if err != nil {
		return errors.Wrap(
			err,
			"unable to unmarshal response to json",
		)
	}
	return nil
}

type NodePathGetter func(node *corev1.Node, path_ string) string

type KubeletClient struct {
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
		return errors.Wrap(
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
		return nil, errors.Wrap(err, "can't test kubelet access")
	}
	if len(nodes) == 0 {
		return nil,
			errors.New("can't test kubelet access. no discovered nodes")
	}

	ctx := context.TODO()
	group, ctx := errgroup.WithContext(ctx)
	once := sync.Once{}
	found := make(chan struct{}, 0)
	done := make(chan struct{}, 0)

	setResult := func(fn NodePathGetter, isApiServer *bool) {
		if isApiServer != nil {
			if *isApiServer {
				logger.Debug(
					"using api-server node proxy to access kubelet metrics",
				)
			} else {
				logger.Debugw(
					"using direct kubelet api through http port",
					"port", client.httpPort,
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
		group.Wait()
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

	*isApiServer = true
	nodeGet, err = client.tryApiServerProxy(node)
	if err == nil {
		return
	}

	*isApiServer = false
	nodeGet, err = client.tryDirectAccess(node)
	if err == nil {
		return
	}

	isApiServer = nil

	return
}

func (client *KubeletClient) tryApiServerProxy(
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
	err := client.testNodeAccess(node, getNodeUrl)
	if err != nil {
		// can't use api-server proxy
		logger.Warnw(
			"can't use api-server proxy to kubelet apis.",
			"error", err,
			"node", node.Name,
		)
		return nil, err
	}
	return getNodeUrl, nil
}

func (client *KubeletClient) tryDirectAccess(
	node *corev1.Node,
) (NodePathGetter, error) {
	getNodeUrl := func(node *corev1.Node, path_ string) string {
		base := fmt.Sprintf("http://%s:%v", GetNodeIP(node), client.httpPort)
		return joinUrl(base, path_)
	}
	err := client.testNodeAccess(node, getNodeUrl)
	if err != nil {
		logger.Warnw(
			"can't use direct kubelet http port.",
			"port", client.httpPort,
			"error", err,
		)
		return nil, err
	}
	return getNodeUrl, nil
}

func (client *KubeletClient) testNodeAccess(
	node *corev1.Node, getNodeUrl NodePathGetter,
) error {
	url_ := getNodeUrl(node, "stats/summary")
	resp, err := client.get(url_)
	if err != nil {
		return errors.Wrapf(err, "node access test failed; node %s", node.Name)
	}

	b, err := readResponseBytes(resp)

	var response interface{}
	err = parseJSON(b, &response)
	if err != nil {
		return errors.Wrapf(err, "node access test failed; node %s", node.Name)
	}
	return nil
}

func (client *KubeletClient) get(url_ string) (*http.Response, error) {
	resp, err := client.restClient.Client.Get(url_)
	if err != nil {
		return nil, fmt.Errorf("Get request to %s failed with error: %w", url_, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
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

	return readResponseBytes(resp)
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
	nodesProvider NodesProvider,
	kube *kuber.Kube,
	kubeletPort string,
) (*KubeletClient, error) {

	restClient, ok := kube.Clientset.RESTClient().(*rest.RESTClient)
	if !ok {
		return nil, fmt.Errorf(
			"invalid cast, please contact developers",
		)
	}

	client := &KubeletClient{

		nodesProvider: nodesProvider,

		kube:       kube,
		restClient: restClient,

		httpPort: kubeletPort,
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
