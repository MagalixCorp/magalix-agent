package events

import (
	"sync"
	"time"

	"github.com/MagalixCorp/magalix-agent/v2/client"
	"github.com/MagalixCorp/magalix-agent/v2/kuber"
	"github.com/MagalixCorp/magalix-agent/v2/proc"
	"github.com/MagalixCorp/magalix-agent/v2/scanner"
	"github.com/MagalixCorp/magalix-agent/v2/utils"
	"github.com/MagalixCorp/magalix-agent/v2/watcher"
	"github.com/MagalixTechnologies/uuid-go"
	"github.com/reconquest/health-go"
	"github.com/reconquest/karma-go"
)

// EventIdentifier entity identifier for events
type EventIdentifier struct {
	Entity   string
	EntityID string
	Kind     string
}

// Eventer event processor
type Eventer struct {
	client   *client.Client
	observer *proc.Observer
	proc     *proc.Proc
	buffer   chan watcher.Event

	last map[EventIdentifier]interface{}

	bufferFlushInterval time.Duration
	bufferSize          int

	skipNamespaces []string
	scanner        *scanner.Scanner

	m sync.Mutex
}

// InitEvents creates a new eventer then starts it
func InitEvents(
	client *client.Client,
	kube *kuber.Kube,
	skipNamespaces []string,
	scanner *scanner.Scanner,
	args map[string]interface{},
) *Eventer {
	eventsBufferFlushInterval := utils.MustParseDuration(args, "--events-buffer-flush-interval")
	eventsBufferSize := utils.MustParseInt(args, "--events-buffer-size")
	eventer := NewEventer(client, kube, skipNamespaces, scanner, eventsBufferFlushInterval, eventsBufferSize)
	eventer.Start()
	return eventer
}

// NewEventer creates a new eventer
func NewEventer(
	client *client.Client,
	kube *kuber.Kube,
	skipNamespaces []string,
	scanner *scanner.Scanner,
	bufferFlushInterval time.Duration,
	bufferSize int,
) *Eventer {
	eventer := &Eventer{
		client:              client,
		bufferSize:          bufferSize,
		bufferFlushInterval: bufferFlushInterval,

		last: make(map[EventIdentifier]interface{}),

		skipNamespaces: skipNamespaces,
		scanner:        scanner,

		m: sync.Mutex{},
	}

	// @TODO
	// eventer/pkg/watcher doesn't support non-package wide logger
	child := client.Logger.NewChildWithPrefix("{eventer}")

	proc.SetLogger(child)

	// @TODO
	// health is passed to watcher, state of health is not propagated to the
	// agent-gateway yet
	health := health.NewHealth()

	observer := proc.NewObserver(
		kube.Clientset,
		kube.ClientV1,
		kube.ClientBatch,
		eventer,
		health,
	)

	// we need extended threadpool only in case of big worker cluster
	const threadpoolSize = 1

	proc := proc.NewProc(
		observer.GetPipePods(), observer.GetPipeReplicas(),
		eventer, eventer, threadpoolSize, health,
	)

	observer.SetSyncCallback(proc.SetSynced)

	eventer.client = client
	eventer.observer = observer
	eventer.proc = proc

	return eventer
}

// Start starts the eventer
func (eventer *Eventer) Start() {
	//go eventer.observer.Start()
	//eventer.proc.Start()
	//eventer.startBatchWriter()
}

// GetApplicationDesiredServices returns desired services of an application
func (eventer *Eventer) GetApplicationDesiredServices(
	id uuid.UUID,
) ([]uuid.UUID, error) {
	eventer.client.Debugf(
		karma.Describe("id", id.String()),
		"{eventer} get application desired services",
	)

	found := false

	services := []uuid.UUID{}
	apps := eventer.scanner.GetApplications()
	for _, app := range apps {
		if app.ID == id {
			for _, service := range app.Services {
				services = append(services, service.ID)
			}

			found = true
			break
		}
	}

	if !found {
		return nil, watcher.ErrorNoSuchEntity
	}

	return services, nil
}

// ChangeStatus change status for an entity
func (eventer *Eventer) ChangeStatus(
	entity string,
	id uuid.UUID,
	status watcher.Status,
	source *watcher.ContainerStatusSource,
) {
	eventer.sendStatus(entity, id, status, source, time.Now())
}

// WriteEvent writes an event
func (eventer *Eventer) WriteEvent(event *watcher.Event) error {
	eventer.client.Tracef(
		karma.Describe("event", eventer.client.TraceJSON(event)),
		"adding event to batch writer buffer",
	)

	// sending events to channel, batch writer is running in background
	eventer.buffer <- *event
	// need to return nil because eventer implements watcher.Database interface
	return nil
}

// WriteEvents writes batch of events
func (eventer *Eventer) WriteEvents(events []*watcher.Event) error {
	// sending events to channel, batch writer is running in background
	for _, event := range events {
		_ = eventer.WriteEvent(event)
	}
	// need to return nil because eventer implements watcher.Database interface
	return nil
}
