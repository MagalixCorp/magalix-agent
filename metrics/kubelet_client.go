package metrics

import (
	"encoding/json"
	"strings"

	"github.com/MagalixCorp/magalix-agent/kuber"
	"github.com/MagalixCorp/magalix-agent/scanner"
	"github.com/MagalixCorp/magalix-agent/utils"
	"github.com/MagalixTechnologies/log-go"
	"github.com/reconquest/karma-go"
)

type NodeAddressGetter func(node *kuber.Node) string

type KubeletClient struct {
	*log.Logger

	singleKubeletHost string

	scanner *scanner.Scanner
	kube    *kuber.Kube
}

func (client *KubeletClient) Get(
	node *kuber.Node,
	path string,
) ([]byte, error) {
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

func (client *KubeletClient) GetJson(
	node *kuber.Node,
	path string,
	response interface{},
) error {
	b, err := client.Get(node, path)
	if err != nil {
		return err
	}

	err = json.Unmarshal(b, &response)
	if err != nil {
		return karma.
			Describe("path", path).
			Describe("node", node.Name).
			Format(
				err,
				"unable to unmarshal response: %s",
				utils.TruncateString(string(b), 100),
			)
	}
	return nil
}

func NewKubeletClient(
	logger *log.Logger,
	scanner *scanner.Scanner,
	kube *kuber.Kube,
) (*KubeletClient, error) {

	client := &KubeletClient{
		Logger: logger,

		scanner: scanner,
		kube:    kube,
	}

	return client, nil
}
