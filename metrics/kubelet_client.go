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

func joinUrl(address, path string) string {
	u, _ := url.Parse(address)
	u.Path = path
	return u.String()
}

type NodeAddressGetter func(node *kuber.Node) string

type KubeletClient struct {
	*log.Logger
	*http.Client

	scanner *scanner.Scanner

	getNodeAddress func(node *kuber.Node) string
}

func (client *KubeletClient) init(
	args map[string]interface{},
) (err error) {
	kubeletAddress, _ := args["--kubelet-address"].(string)
	kubeletPort, _ := args["--kubelet-port"].(string)

	if kubeletAddress != "" && strings.HasPrefix(kubeletAddress, "/") {
		return karma.Format(
			nil,
			"invalid kubelet address %s. should not start with /",
			kubeletAddress,
		)
	}

	getNodeAddress, err := client.discoverNodesAddress(kubeletAddress, kubeletPort)
	if err != nil {
		return karma.Format(
			err,
			"unable to get working kubelet address",
		)
	}

	client.getNodeAddress = getNodeAddress
	return nil
}

func (client *KubeletClient) discoverNodesAddress(
	kubeletAddress, httpPort string,
) (getNodeKubeletAddress NodeAddressGetter, err error) {

	if kubeletAddress != "" {
		getNodeKubeletAddress = func(node *kuber.Node) string {
			return kubeletAddress
		}

		err = client.testNodeAccess(nil, getNodeKubeletAddress)

		if err == nil {
			client.Infof(
				karma.Describe("address", kubeletAddress),
				"using single forced kubeletAddress",
			)
		}

		return
	}

	nodes := client.scanner.GetNodes()
	if len(nodes) == 0 {
		return nil, karma.Format(
			nil,
			"can't test kubelet ports. no discovered nodes",
		)
	}

	ctx := context.TODO()
	group, ctx := errgroup.WithContext(ctx)
	once := sync.Once{}
	found := make(chan struct{}, 0)
	done := make(chan struct{}, 0)

	for _, node := range client.scanner.GetNodes() {
		func(n kuber.Node) {
			group.Go(func() error {
				getNodeAddress, err := client.discoverNodeAddress(&n, httpPort)
				if err == nil {
					once.Do(func() {
						getNodeKubeletAddress = getNodeAddress
						close(found)
					})
				}
				return err
			})
		}(node)
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
	node *kuber.Node, httpPort string,
) (getNodeKubeletAddress NodeAddressGetter, err error) {

	getNodeKubeletAddress = func(node *kuber.Node) string {
		return fmt.Sprintf("https://%s:%v", node.IP, node.KubeletPort)
	}
	err = client.testNodeAccess(node, getNodeKubeletAddress)
	if err == nil {
		client.Infof(
			karma.Describe("port", "<auto>"),
			"using kubelet TLS port",
		)
	} else {
		//	can't use TLS port for some reason
		client.Errorf(
			err,
			"can't use kubelet TLS port. "+
				"falling back to http readonly port: %s",
			httpPort,
		)

		getNodeKubeletAddress = func(node *kuber.Node) string {
			return fmt.Sprintf("http://%s:%v", node.IP, httpPort)
		}
		err = client.testNodeAccess(node, getNodeKubeletAddress)
		if err == nil {
			client.Infof(
				karma.Describe("port", httpPort),
				"using kubelet http readonly port",
			)
		} else {
			getNodeKubeletAddress = nil
			client.Errorf(
				err,
				"can't use kubelet http readonly port %s",
				httpPort,
			)
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
	ctx := karma.Describe("url", url)
	response, err := client.Client.Get(url)
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

func (client *KubeletClient) Get(node *kuber.Node, path string) ([]byte, error) {
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

	tlsConfig := &tls.Config{
		InsecureSkipVerify: args["--kubelet-insecure"].(bool),
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

		scanner: scanner,
	}

	err := client.init(args)
	if err != nil {
		return nil, karma.Format(
			err,
			"unable to init kubelet client",
		)
	}

	return client, nil
}
