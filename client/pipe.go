package client

import (
	"sync"
	"time"

	"github.com/MagalixCorp/magalix-agent/v2/proto"
	"github.com/MagalixTechnologies/log-go"
	"github.com/reconquest/karma-go"
)

// PipeSender interface for sender
type PipeSender interface {
	Send(kind proto.PacketKind, in interface{}, out interface{}) error
}

// Pipe pipe
type Pipe struct {
	cond *sync.Cond

	logger  *log.Logger
	sender  PipeSender
	storage PipeStore
}

// NewPipe creates a new pipe
func NewPipe(sender PipeSender, logger *log.Logger) *Pipe {
	return &Pipe{
		cond: sync.NewCond(&sync.Mutex{}),

		logger:  logger,
		sender:  sender,
		storage: NewDefaultPipeStore(),
	}
}

// Send pushes a packet to the pipe to be sent
func (p *Pipe) Send(pack Package) int {
	pack.time = time.Now()
	ret := p.storage.Add(&pack)
	p.cond.Broadcast()
	return ret
}

// Start start multiple workers for sending packages
func (p *Pipe) Start(workers int) {
	for i := 0; i < workers; i++ {
		p.start()
	}
}

// start start a single worker
func (p *Pipe) start() {
	go func() {
		for {
			p.cond.L.Lock()
			pack := p.storage.Pop()
			if pack == nil {
				p.cond.Wait()
				p.cond.L.Unlock()
				continue
			}
			p.cond.L.Unlock()

			ctx := karma.Describe("kind", pack.Kind).
				Describe("diff", time.Now().Sub(pack.time)).
				Describe("remaining", p.storage.Len())

			p.logger.Debugf(ctx, "sending packet")

			err := p.sender.Send(pack.Kind, pack.Data, nil)
			ctx = ctx.Describe("diff", time.Now().Sub(pack.time))
			if err != nil {
				p.storage.Add(pack)
				ctx = ctx.Describe("remaining", p.storage.Len())
				p.logger.Errorf(ctx.Reason(err), "error sending packet")
			} else {
				ctx = ctx.Describe("remaining", p.storage.Len())
				p.logger.Infof(ctx, "completed sending packet")
			}
		}
	}()
}

// Len gets the number of pending packages
func (p *Pipe) Len() int {
	return p.storage.Len()
}
