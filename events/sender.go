package events

import (
	"time"

	"github.com/MagalixCorp/magalix-agent/v2/client"
	"github.com/MagalixCorp/magalix-agent/v2/proto"
	"github.com/MagalixCorp/magalix-agent/v2/utils"
	"github.com/MagalixCorp/magalix-agent/v2/watcher"
	"github.com/MagalixTechnologies/core/logger"
	"github.com/MagalixTechnologies/uuid-go"
)

func (eventer *Eventer) startBatchWriter() {
	eventer.buffer = make(chan watcher.Event, eventer.bufferSize)

	go func() {
		ticker := time.NewTicker(eventer.bufferFlushInterval)
		events := []watcher.Event{}

		for {
			timeout := false
			select {
			case event := <-eventer.buffer:
				events = append(events, event)
			case <-ticker.C:
				timeout = true
			}

			if len(events) == 0 {
				continue
			}

			if timeout || len(events) > eventer.bufferSize {
				go eventer.sendEvents(events)

				events = []watcher.Event{}
			}
		}
	}()
}

func (eventer *Eventer) sendEvents(events []watcher.Event) {
	newEvents := make([]watcher.Event, 0, len(events))
	eventer.m.Lock()
	defer eventer.m.Unlock()
	for i := range events {
		event := &events[i]
		identifier := EventIdentifier{event.Entity, event.EntityID, event.Kind}
		if last, ok := eventer.last[identifier]; !ok || last != events[i].Value {
			// potential memory leak
			eventer.last[identifier] = event.Value
			newEvents = append(newEvents, *event)
		}
	}
	if len(newEvents) > 0 {
		logger.Debugw("sending events",
			"timestamp", events[0].Timestamp,
			"count", len(events),
			"new", len(newEvents),
		)
		eventer.sendEventsBatch(events)
		logger.Infow("events sent",
			"timestamp", events[0].Timestamp,
			"count", len(events),
			"new", len(newEvents),
		)
	} else if len(events) > 0 {
		logger.Debugw("skipping sending events, nothing new",
			"timestamp", events[0].Timestamp,
			"count", len(events),
			"new", len(newEvents),
		)
	}
}

// sendEventsBatch bulk send events
func (eventer *Eventer) sendEventsBatch(events []watcher.Event) {
	eventer.client.Pipe(client.Package{
		Kind:        proto.PacketKindEventsStoreRequest,
		ExpiryTime:  utils.After(2 * time.Hour),
		ExpiryCount: 100,
		Priority:    6,
		Retries:     10,
		Data:        proto.PacketEventsStoreRequest(events),
	})
}

// sendStatus sends status updates
func (eventer *Eventer) sendStatus(
	entity string,
	id uuid.UUID,
	status watcher.Status,
	source *watcher.ContainerStatusSource,
	timestamp time.Time,
) {
	logger.Debugw("changing status",
		"entity", entity,
		"id", id.String(),
		"status", status.String(),
	)

	eventer.client.PipeStatus(client.Package{
		Kind:        proto.PacketKindStatusStoreRequest,
		ExpiryTime:  utils.After(2 * time.Hour),
		ExpiryCount: 1000,
		// given low priority as these are generated a lot and it blocks lower priority packages
		Priority: 12,
		Retries:  10,
		Data: proto.PacketStatusStoreRequest{
			Entity:    entity,
			EntityID:  id,
			Status:    status,
			Source:    source,
			Timestamp: timestamp,
		},
	})
}
