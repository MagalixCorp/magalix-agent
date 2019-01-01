package events

import (
	"time"

	"github.com/MagalixCorp/magalix-agent/client"
	"github.com/MagalixCorp/magalix-agent/proto"
	"github.com/MagalixCorp/magalix-agent/utils"
	"github.com/MagalixCorp/magalix-agent/watcher"
	"github.com/MagalixTechnologies/uuid-go"
	"github.com/reconquest/karma-go"
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
		eventer.client.Infof(
			karma.Describe("timestamp", events[0].Timestamp).
				Describe("count", len(events)).
				Describe("new", len(newEvents)),
			"sending events",
		)
		eventer.sendEventsBatch(events)
		eventer.client.Infof(karma.Describe("timestamp", events[0].Timestamp), "events sent")
	} else if len(events) > 0 {
		eventer.client.Debugf(
			karma.Describe("timestamp", events[0].Timestamp).
				Describe("count", len(events)).
				Describe("new", len(newEvents)),
			"skipping sending events, nothing new",
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
) {
	eventer.client.Debugf(
		karma.
			Describe("entity", entity).
			Describe("id", id.String()).
			Describe("status", status.String()),

		"{eventer} changing status",
	)

	eventer.client.Pipe(client.Package{
		Kind:        proto.PacketKindStatusStoreRequest,
		ExpiryTime:  utils.After(2 * time.Hour),
		ExpiryCount: 1000,
		Priority:    6,
		Retries:     10,
		Data: proto.PacketStatusStoreRequest{
			Entity:   entity,
			EntityID: id,
			Status:   status,
			Source:   source,
		},
	})
}
