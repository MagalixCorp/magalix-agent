package scalar2

import (
	"time"

	"github.com/MagalixCorp/magalix-agent/v2/kuber"
	"github.com/MagalixTechnologies/log-go"
)

func InitScalars(
	logger *log.Logger,
	kube *kuber.Kube,
	observer_ *kuber.Observer,
	dryRun bool,
) {

	sl := NewScannerListener(logger, observer_)
	oomKilledProcessor := NewOOMKillsProcessor(
		logger,
		kube,
		observer_,
		time.Second,
		dryRun,
	)

	sl.AddPodListener(oomKilledProcessor)

	go oomKilledProcessor.Start()
	go sl.Start()
}
