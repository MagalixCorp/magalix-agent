package scanner

import (
	"github.com/MagalixCorp/magalix-agent/kuber"
	"github.com/MagalixCorp/magalix-agent/proto"
)

// SendApplications sends scanned applications
func (scanner *Scanner) SendApplications(applications []*Application) {
	scanner.client.WithBackoff(func() error {
		var response proto.PacketApplicationsStoreResponse
		return scanner.client.Send(proto.PacketKindApplicationsStoreRequest, PacketApplications(applications), &response)
	})
}

// SendNodes sends scanned nodes
func (scanner *Scanner) SendNodes(nodes []kuber.Node) {
	scanner.client.WithBackoff(func() error {
		var response proto.PacketNodesStoreResponse
		return scanner.client.Send(proto.PacketKindNodesStoreRequest, PacketNodes(nodes), &response)
	})
}

// SendAnalysisData sends analysis data if the user opts in
func (scanner *Scanner) SendAnalysisData(data map[string]interface{}) {
	if scanner.optInAnalysisData {
		scanner.client.SendRaw(data)
	}
}
