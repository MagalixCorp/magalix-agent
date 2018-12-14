package proc

import (
	"sync"

	uuid "github.com/MagalixTechnologies/uuid-go"
)

// States global state for the pricessor
type States struct {
	apps map[uuid.UUID]*AppState
	*sync.Mutex
}

// NewStates returns a new states
func NewStates() *States {
	return &States{
		apps:  map[uuid.UUID]*AppState{},
		Mutex: &sync.Mutex{},
	}
}

// GetApp gets app by id
func (states *States) GetApp(id uuid.UUID) (*AppState, bool) {
	app, ok := states.apps[id]
	return app, ok
}

// NewApp creates a new app
func (states *States) NewApp(
	id uuid.UUID, accountID uuid.UUID,
) *AppState {
	app := &AppState{
		accountID:       accountID,
		services:        map[uuid.UUID]*ServiceState{},
		desiredServices: []uuid.UUID{},
		RWMutex:         &sync.RWMutex{},
	}

	states.apps[id] = app

	return app
}
