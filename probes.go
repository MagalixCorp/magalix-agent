package main

import (
	"net/http"

	"github.com/MagalixTechnologies/core/logger"
)

const (
	liveness  = "/live"
	readiness = "/ready"
)

type ProbesServer struct {
	address string

	Authorized bool
}

func NewProbesServer(address string) *ProbesServer {
	return &ProbesServer{
		address:    address,
		Authorized: false,
	}
}

func (p *ProbesServer) Start() error {
	http.HandleFunc(liveness, p.livenessProbeHandler)
	http.HandleFunc(readiness, p.readinessProbeHandler)

	logger.Infow("Starting server....", "address", p.address)
	defer func() {
		logger.Info("Probes server started")
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
