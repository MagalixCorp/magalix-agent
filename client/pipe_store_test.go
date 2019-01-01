package client

import (
	"sync"
	"testing"
	"time"

	"github.com/MagalixCorp/magalix-agent/proto"
)

func after(d time.Duration) *time.Time {
	t := time.Now().Add(d)
	return &t
}

func TestDefaultPipeStore_Add(t *testing.T) {
	type Inp struct {
		pack *Package
		want int
	}
	tests := []struct {
		name string
		inps []Inp
	}{
		{
			name: "adding a package",
			inps: []Inp{
				{
					pack: &Package{
						Kind:        proto.PacketKindHello,
						ExpiryTime:  after(10 * time.Second),
						ExpiryCount: 2,
						Priority:    1,
						Retries:     4,
						Data:        "asd",
					},
					want: 0,
				},
			},
		},
		{
			name: "adding a time expired package",
			inps: []Inp{
				{
					pack: &Package{
						Kind:        proto.PacketKindHello,
						ExpiryTime:  after(-10 * time.Second),
						ExpiryCount: 2,
						Priority:    1,
						Retries:     4,
						Data:        "asd",
					},
					want: 1,
				},
			},
		},
		{
			name: "adding a non expired package with expiry count 1",
			inps: []Inp{
				{
					pack: &Package{
						Kind:        proto.PacketKindHello,
						ExpiryTime:  after(10 * time.Second),
						ExpiryCount: 1,
						Priority:    1,
						Retries:     4,
						Data:        "asd",
					},
					want: 0,
				},
			},
		},
		{
			name: "adding a non expired package with expiry count 0",
			inps: []Inp{
				{
					pack: &Package{
						Kind:        proto.PacketKindHello,
						ExpiryTime:  after(10 * time.Second),
						ExpiryCount: 0,
						Priority:    1,
						Retries:     4,
						Data:        "asd",
					},
					want: 0,
				},
			},
		},
		{
			name: "adding a second package after one with expiry count 1",
			inps: []Inp{
				{
					pack: &Package{
						Kind:        proto.PacketKindHello,
						ExpiryTime:  after(10 * time.Second),
						ExpiryCount: 1,
						Priority:    1,
						Retries:     4,
						Data:        "asd",
					},
					want: 0,
				},
				{
					pack: &Package{
						Kind:        proto.PacketKindHello,
						ExpiryTime:  after(10 * time.Second),
						ExpiryCount: 2,
						Priority:    1,
						Retries:     4,
						Data:        "asd",
					},
					want: 1,
				},
			},
		},
		{
			name: "adding a second package after one with expiry count 0",
			inps: []Inp{
				{
					pack: &Package{
						Kind:        proto.PacketKindHello,
						ExpiryTime:  after(10 * time.Second),
						ExpiryCount: 0,
						Priority:    1,
						Retries:     4,
						Data:        "asd",
					},
					want: 0,
				},
				{
					pack: &Package{
						Kind:        proto.PacketKindHello,
						ExpiryTime:  after(10 * time.Second),
						ExpiryCount: 2,
						Priority:    1,
						Retries:     4,
						Data:        "asd",
					},
					want: 0,
				},
			},
		},
		{
			name: "adding a third package after one with expiry count 2",
			inps: []Inp{
				{
					pack: &Package{
						Kind:        proto.PacketKindHello,
						ExpiryTime:  after(10 * time.Second),
						ExpiryCount: 2,
						Priority:    1,
						Retries:     4,
						Data:        "asd",
					},
					want: 0,
				},
				{
					pack: &Package{
						Kind:        proto.PacketKindHello,
						ExpiryTime:  after(10 * time.Second),
						ExpiryCount: 2,
						Priority:    1,
						Retries:     4,
						Data:        "asd",
					},
					want: 0,
				},
				{
					pack: &Package{
						Kind:        proto.PacketKindHello,
						ExpiryTime:  after(10 * time.Second),
						ExpiryCount: 1,
						Priority:    1,
						Retries:     4,
						Data:        "asd",
					},
					want: 2,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewDefaultPipeStore()
			for i, inp := range tt.inps {
				if got := s.Add(inp.pack); got != inp.want {
					t.Errorf("case %d (%dth) DefaultPipeStore.Add() = %v, want %v", i, i+1, got, inp.want)
				}
			}
		})
	}
}

func TestDefaultPipeStore_Peek(t *testing.T) {
	type Inp struct {
		pack  *Package
		want  int
		order []int
	}
	tests := []struct {
		name string
		inps []Inp
	}{
		{
			name: "adding a second package after one with expiry count 1",
			inps: []Inp{
				{
					pack: &Package{
						Kind:        proto.PacketKindHello,
						ExpiryTime:  after(10 * time.Second),
						ExpiryCount: 1,
						Priority:    1,
						Retries:     4,
						Data:        "first",
					},
					want: 0,
				},
				{
					pack: &Package{
						Kind:        proto.PacketKindHello,
						ExpiryTime:  after(10 * time.Second),
						ExpiryCount: 2,
						Priority:    1,
						Retries:     4,
						Data:        "second",
					},
					want:  1,
					order: []int{0, 1, 2},
				},
			},
		},
		{
			name: "adding two packages with the same priorities",
			inps: []Inp{
				{
					pack: &Package{
						Kind:        proto.PacketKindHello,
						ExpiryTime:  after(10 * time.Second),
						ExpiryCount: 2,
						Priority:    1,
						Retries:     4,
						Data:        "first",
					},
					want:  0,
					order: []int{0, 1, 2},
				},
				{
					pack: &Package{
						Kind:        proto.PacketKindHello,
						ExpiryTime:  after(10 * time.Second),
						ExpiryCount: 2,
						Priority:    1,
						Retries:     4,
						Data:        "second",
					},
					want: 0,
				},
			},
		},
		{
			name: "adding two packages with different priorities",
			inps: []Inp{
				{
					pack: &Package{
						Kind:        proto.PacketKindHello,
						ExpiryTime:  after(10 * time.Second),
						ExpiryCount: 2,
						Priority:    1,
						Retries:     4,
						Data:        "first",
					},
					want: 0,
				},
				{
					pack: &Package{
						Kind:        proto.PacketKindHello,
						ExpiryTime:  after(10 * time.Second),
						ExpiryCount: 2,
						Priority:    0,
						Retries:     4,
						Data:        "second",
					},
					want:  0,
					order: []int{0, 1, 2},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewDefaultPipeStore()
			out := make([]*Package, 0)
			for i, inp := range tt.inps {
				for _, order := range inp.order {
					if order >= 0 {
						needed := order - len(out) + 1
						if needed > 0 {
							out = append(out, make([]*Package, needed)...)
						}
						out[order] = inp.pack
					}
				}
				if got := s.Add(inp.pack); got != inp.want {
					t.Errorf("case %d (%dth) DefaultPipeStore.Add() = %v, want %v", i, i+1, got, inp.want)
				}
			}
			for i, o := range out {
				if got := s.Peek(); got != o {
					t.Errorf("case %d (%dth) DefaultPipeStore.Peek() = %v, want %v", i, i+1, got, o)
				}
			}
		})
	}
}

func TestDefaultPipeStore_Peek_Ack(t *testing.T) {
	type Inp struct {
		pack  *Package
		want  int
		order int
	}
	tests := []struct {
		name string
		inps []Inp
	}{
		{
			name: "adding a package",
			inps: []Inp{
				{
					pack: &Package{
						Kind:        proto.PacketKindHello,
						ExpiryTime:  after(10 * time.Second),
						ExpiryCount: 2,
						Priority:    1,
						Retries:     4,
						Data:        "asd",
					},
					want:  0,
					order: 0,
				},
			},
		},
		{
			name: "adding a time expired package",
			inps: []Inp{
				{
					pack: &Package{
						Kind:        proto.PacketKindHello,
						ExpiryTime:  after(-10 * time.Second),
						ExpiryCount: 2,
						Priority:    1,
						Retries:     4,
						Data:        "asd",
					},
					want:  1,
					order: -1,
				},
			},
		},
		{
			name: "adding a non expired package with expiry count 1",
			inps: []Inp{
				{
					pack: &Package{
						Kind:        proto.PacketKindHello,
						ExpiryTime:  after(10 * time.Second),
						ExpiryCount: 1,
						Priority:    1,
						Retries:     4,
						Data:        "asd",
					},
					want:  0,
					order: 0,
				},
			},
		},
		{
			name: "adding a non expired package with expiry count 0",
			inps: []Inp{
				{
					pack: &Package{
						Kind:        proto.PacketKindHello,
						ExpiryTime:  after(10 * time.Second),
						ExpiryCount: 0,
						Priority:    1,
						Retries:     4,
						Data:        "asd",
					},
					want:  0,
					order: 0,
				},
			},
		},
		{
			name: "adding a second package after one with expiry count 1",
			inps: []Inp{
				{
					pack: &Package{
						Kind:        proto.PacketKindHello,
						ExpiryTime:  after(10 * time.Second),
						ExpiryCount: 1,
						Priority:    1,
						Retries:     4,
						Data:        "asd",
					},
					want:  0,
					order: -1,
				},
				{
					pack: &Package{
						Kind:        proto.PacketKindHello,
						ExpiryTime:  after(10 * time.Second),
						ExpiryCount: 2,
						Priority:    1,
						Retries:     4,
						Data:        "asd",
					},
					want:  1,
					order: 0,
				},
			},
		},
		{
			name: "adding a second package after one with expiry count 0",
			inps: []Inp{
				{
					pack: &Package{
						Kind:        proto.PacketKindHello,
						ExpiryTime:  after(10 * time.Second),
						ExpiryCount: 0,
						Priority:    1,
						Retries:     4,
						Data:        "first",
					},
					want:  0,
					order: 0,
				},
				{
					pack: &Package{
						Kind:        proto.PacketKindHello,
						ExpiryTime:  after(10 * time.Second),
						ExpiryCount: 2,
						Priority:    1,
						Retries:     4,
						Data:        "second",
					},
					want:  0,
					order: 1,
				},
			},
		},
		{
			name: "adding a third package after one with expiry count 2",
			inps: []Inp{
				{
					pack: &Package{
						Kind:        proto.PacketKindHello,
						ExpiryTime:  after(10 * time.Second),
						ExpiryCount: 2,
						Priority:    1,
						Retries:     4,
						Data:        "first",
					},
					want:  0,
					order: -1,
				},
				{
					pack: &Package{
						Kind:        proto.PacketKindHello,
						ExpiryTime:  after(10 * time.Second),
						ExpiryCount: 2,
						Priority:    1,
						Retries:     4,
						Data:        "second",
					},
					want:  0,
					order: 0,
				},
				{
					pack: &Package{
						Kind:        proto.PacketKindHello,
						ExpiryTime:  after(10 * time.Second),
						ExpiryCount: 1,
						Priority:    1,
						Retries:     4,
						Data:        "third",
					},
					want:  2,
					order: -1,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewDefaultPipeStore()
			out := make([]*Package, len(tt.inps))
			for i, inp := range tt.inps {
				if inp.order >= 0 {
					out[inp.order] = inp.pack
				}
				if got := s.Add(inp.pack); got != inp.want {
					t.Errorf("case %d (%dth) DefaultPipeStore.Add() = %v, want %v", i, i+1, got, inp.want)
				}
			}
			for i, o := range out {
				got := s.Peek()
				if got != o {
					t.Errorf("case %d (%dth) DefaultPipeStore.Add() = %v, want %v", i, i+1, got, o)
				} else if got != nil {
					s.Ack(got)
				}
			}
		})
	}
}

func TestDefaultPipeStore_Ack(t *testing.T) {
	type fields struct {
		Mutex   sync.Mutex
		removed int
		pq      *PriorityQueue
		kinds   map[proto.PacketKind][]*Package
	}
	type args struct {
		pack *Package
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &DefaultPipeStore{
				Mutex:   tt.fields.Mutex,
				removed: tt.fields.removed,
				pq:      tt.fields.pq,
				kinds:   tt.fields.kinds,
			}
			s.Ack(tt.args.pack)
		})
	}
}
