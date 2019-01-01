package client

import (
	"sync"

	"github.com/MagalixCorp/magalix-agent/proto"
)

// PipeSender interface for sender
type PipeSender interface {
	Send(kind proto.PacketKind, in interface{}, out interface{}) error
}

// Pipe pipe
type Pipe struct {
	cond *sync.Cond

	sender  PipeSender
	storage PipeStore
}

// NewPipe creates a new pipe
func NewPipe(sender PipeSender) *Pipe {
	return &Pipe{
		cond: sync.NewCond(&sync.Mutex{}),

		sender:  sender,
		storage: NewDefaultPipeStore(),
	}
}

// Send pushes a packet to the pipe to be sent
func (p *Pipe) Send(pack Package) int {
	ret := p.storage.Add(&pack)
	p.cond.Broadcast()
	return ret
}

// Start start sending packages
func (p *Pipe) Start() {
	go func() {
		for {
			p.cond.L.Lock()
			pack := p.storage.Peek()
			if pack == nil {
				p.cond.Wait()
				p.cond.L.Unlock()
				continue
			}
			p.cond.L.Unlock()
			err := p.sender.Send(pack.Kind, pack.Data, nil)
			if err == nil {
				p.storage.Ack(pack)
			}
		}
	}()
}

// Len gets the number of pending packages
func (p *Pipe) Len() int {
	return p.storage.Len()
}