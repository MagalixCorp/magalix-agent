package scanner

import (
	"github.com/MagalixCorp/magalix-agent/client"
	"github.com/MagalixCorp/magalix-agent/kuber"
	"github.com/MagalixCorp/magalix-agent/proto"
)

// SendApplications sends scanned applications
func (scanner *Scanner) SendApplications(applications []*Application) {
	if scanner.enableSender {
		scanner.client.Pipe(client.Package{
			Kind:        proto.PacketKindApplicationsStoreRequest,
			ExpiryTime:  nil,
			ExpiryCount: 1,
			Priority:    2,
			Retries:     10,
			Data:        PacketApplications(applications),
		})
	}
}

// SendNodes sends scanned nodes
func (scanner *Scanner) SendNodes(nodes []kuber.Node) {
	if scanner.enableSender {
		scanner.client.Pipe(client.Package{
			Kind:        proto.PacketKindNodesStoreRequest,
			ExpiryTime:  nil,
			ExpiryCount: 1,
			Priority:    1,
			Retries:     10,
			Data:        PacketNodes(nodes),
		})
	}
}

// SendAnalysisData sends analysis data if the user opts in
func (scanner *Scanner) SendAnalysisData(data map[string]interface{}) {
	scanner.analysisDataSender(data)
}
