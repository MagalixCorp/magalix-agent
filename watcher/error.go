package watcher

import (
	"errors"
)

var (
	// ErrorNoSuchEntity to specify not found entity
	ErrorNoSuchEntity = errors.New("no such entity")
)
