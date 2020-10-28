package scalar2

import (
	"time"

	"github.com/MagalixCorp/magalix-agent/v2/kuber"
)

func InitScalars(
	kube *kuber.Kube,
	observer_ *kuber.Observer,
	dryRun bool,
) {

	sl := NewScannerListener(observer_)
	oomKilledProcessor := NewOOMKillsProcessor(
		kube,
		observer_,
		time.Second,
		dryRun,
	)

	sl.AddPodListener(oomKilledProcessor)

	go oomKilledProcessor.Start()
	go sl.Start()
}
