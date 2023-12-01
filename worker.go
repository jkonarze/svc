package svc

import (
	"github.com/rs/zerolog"
)

// Worker defines a SVC worker.
type Worker interface {
	Init(logger *zerolog.Logger) error
	Run() error
	Terminate() error
}

// Aliver defines a worker that can report his livez status.
type Aliver interface {
	Alive() error
}

// Healther defines a worker that can report his healthz status.
type Healther interface {
	Healthy() error
}
