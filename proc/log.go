package proc

import (
	"github.com/MagalixTechnologies/log-go"
	"github.com/reconquest/karma-go"
)

var (
	logger *log.Logger
)

// SetLogger setter for the global logger
func SetLogger(value *log.Logger) {
	logger = value
}

func tracef(
	context *karma.Context,
	message string,
	args ...interface{},
) {
	logger.Tracef(context, message, args...)
}

func debugf(
	context *karma.Context,
	message string,
	args ...interface{},
) {
	logger.Debugf(context, message, args...)
}

func infof(
	context *karma.Context,
	message string,
	args ...interface{},
) {
	logger.Infof(context, message, args...)
}

func warningf(
	err error,
	message string,
	args ...interface{},
) {
	logger.Warningf(err, message, args...)
}

func errorf(
	err error,
	message string,
	args ...interface{},
) {
	logger.Errorf(err, message, args...)
}
