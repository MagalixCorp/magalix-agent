package events

import (
	"time"

	"github.com/MagalixCorp/magalix-agent/proto"
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
	eventer.client.WithBackoff(func() error {
		var response proto.PacketEventsStoreResponse
		return eventer.client.Send(proto.PacketKindEventsStoreRequest, proto.PacketEventsStoreRequest(events), &response)
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

	// TODO: make non blocking, it was changed to be blocking as a quick fix to a race condition where old values override new ones
	eventer.client.WithBackoff(func() error {
		var response proto.PacketStatusStoreResponse
		return eventer.client.Send(proto.PacketKindStatusStoreRequest, proto.PacketStatusStoreRequest{
			Entity:   entity,
			EntityID: id,
			Status:   status,
			Source:   source,
		}, &response)
	})
}
