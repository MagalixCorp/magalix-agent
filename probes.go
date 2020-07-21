package main

import (
	"github.com/MagalixTechnologies/log-go"
	"net/http"
)

const (
	liveness  = "/live"
	readiness = "/ready"
)

type ProbesServer struct {
	address string
	logger  *log.Logger

	Authorized bool
}

func NewProbesServer(address string, logger *log.Logger) *ProbesServer {
	return &ProbesServer{
		address:    address,
		logger:     logger,
		Authorized: false,
	}
}

func (p *ProbesServer) Start() error {
	http.HandleFunc(liveness, p.livenessProbeHandler)
	http.HandleFunc(readiness, p.readinessProbeHandler)

	p.logger.Infof(nil, "Starting server at address %s. Liveness probe is %s, Readiness probe is %s", p.address, liveness, readiness)
	defer func() {
		p.logger.Info("Probes server started")
	}()

	return http.ListenAndServe(p.address, nil)
}

func (p *ProbesServer) livenessProbeHandler(w http.ResponseWriter, req *http.Request) {
	w.WriteHeader(200)
}

func (p *ProbesServer) readinessProbeHandler(w http.ResponseWriter, req *http.Request) {
	if p.Authorized {
		w.WriteHeader(200)
	} else {
		w.WriteHeader(503)
	}
}
