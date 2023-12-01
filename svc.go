package svc

import (
	"context"
	"errors"
	"fmt"
	"github.com/rs/zerolog"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/avast/retry-go/v4"
	"go.uber.org/zap"
)

const (
	defaultTerminationGracePeriod = 15 * time.Second
	defaultTerminationWaitPeriod  = 0 * time.Second
)

// SVC defines the worker life-cycle manager. It holds service metadata, router,
// logger, and the workers.
type SVC struct {
	Name    string
	Version string

	Router *http.ServeMux

	TerminationGracePeriod time.Duration
	TerminationWaitPeriod  time.Duration
	signals                chan os.Signal

	logger             *zerolog.Logger
	zapOpts            []zap.Option
	stdLogger          *log.Logger
	atom               zap.AtomicLevel
	loggerRedirectUndo func()

	workers             map[string]Worker
	workerInitRetryOpts map[string][]retry.Option
	workersAdded        []string
	workersInitialized  []string
}

// New instantiates a new service by parsing configuration and initializing a
// logger.
func New(name, version string, opts ...Option) (*SVC, error) {
	s := &SVC{
		Name:    name,
		Version: version,

		Router: http.NewServeMux(),

		TerminationGracePeriod: defaultTerminationGracePeriod,
		TerminationWaitPeriod:  defaultTerminationWaitPeriod,
		signals:                make(chan os.Signal, 3),

		workers:             map[string]Worker{},
		workersAdded:        []string{},
		workersInitialized:  []string{},
		workerInitRetryOpts: map[string][]retry.Option{},
	}

	// Apply options
	for _, o := range opts {
		if err := o(s); err != nil {
			return nil, err
		}
	}

	return s, nil
}

// AddWorker adds a named worker to the service. Added workers order is
// maintained.
func (s *SVC) AddWorker(name string, w Worker) {
	if _, exists := s.workers[name]; exists {
		s.logger.
			Fatal().
			Any("name", name).
			Msg("Duplicate worker names!")
	}
	if _, ok := w.(Healther); !ok {
		s.logger.
			Info().
			Any("worker", name).
			Msg("Worker does not implement Healther interface")
	}
	if _, ok := w.(Aliver); !ok {
		s.logger.
			Info().
			Any("worker", name).
			Msg("Worker does not implement Aliver interface")
	}
	// Track workers as ordered set to initialize them in order.
	s.workersAdded = append(s.workersAdded, name)
	s.workers[name] = w
}

// AddWorkerWithInitRetry adds a named worker to the service.
// If the worker-initialization fails, it will be retried according to specified options.
func (s *SVC) AddWorkerWithInitRetry(name string, w Worker, retryOpts []retry.Option) {
	s.AddWorker(name, w)
	s.workerInitRetryOpts[name] = retryOpts
}

// Run runs the service until either receiving an interrupt or a worker
// terminates.
func (s *SVC) Run() {
	s.logger.
		Info().
		Msg("Starting up service")

	defer func() {
		s.logger.
			Info().
			Any("termination_grace_period", s.TerminationGracePeriod).
			Msg("Shutting down service")
		s.terminateWorkers()
		s.logger.
			Info().
			Msg("Service shutdown completed")
	}()

	// Initializing workers in added order.
	for _, name := range s.workersAdded {
		s.logger.
			Debug().
			Any("worker", name).
			Msg("Initializing worker")
		w := s.workers[name]
		var err error
		if opts, ok := s.workerInitRetryOpts[name]; ok {
			//nolint:scopelint
			err = retry.Do(func() error { return w.Init(s.logger) }, opts...)
		} else {
			err = w.Init(s.logger)
		}
		if err != nil {
			s.logger.
				Error().
				Any("worker", name).
				Msg("Could not initialize worker")
			return
		}
		s.workersInitialized = append(s.workersInitialized, name)
	}

	errs := make(chan error)
	wg := sync.WaitGroup{}
	for name, w := range s.workers {
		wg.Add(1)
		go func(name string, w Worker) {
			defer s.recoverWait(name, &wg, errs)
			if err := w.Run(); err != nil {
				err = fmt.Errorf("worker %s exited: %w", name, err)
				errs <- err
			}
		}(name, w)
	}

	signal.Notify(s.signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	select {
	case err := <-errs:
		if !errors.Is(err, context.Canceled) {
			s.logger.
				Fatal().
				Err(err).
				Msg("Worker Init/Run failure")
		}
		s.logger.
			Warn().
			Err(err).
			Msg("Worker context canceled")
	case sig := <-s.signals:
		s.logger.
			Warn().
			Any("signal", sig.String()).
			Msg("Caught signal")
	case <-waitGroupToChan(&wg):
		s.logger.
			Info().
			Msg("All workers have finished")
	}
}

// Shutdown signals the framework to terminate any already started workers and
// shutdown the service.
// The call is non-blocking. Terminating the workers comes with the guarantees
// as the `Run` method: All workers are given a total terminate grace-period
// until the service goes ahead completes the shutdown phase.
func (s *SVC) Shutdown() {
	s.signals <- syscall.SIGTERM
}

// MustInit is a convenience function to check for and halt on errors.
func MustInit(s *SVC, err error) *SVC {
	if err != nil {
		if s == nil || s.logger == nil {
			panic(err)
		}
		s.logger.
			Fatal().
			Err(err).
			Msg("Service initialization failed")
		return nil
	}
	return s
}

// Logger returns the service's logger. Logger might be nil if New() fails.
func (s *SVC) Logger() *zerolog.Logger {
	return s.logger
}

func (s *SVC) terminateWorkers() {
	s.logger.
		Info().
		Any("termination_grace_period", s.TerminationGracePeriod).
		Msg("Terminating workers down service")

	// terminate only initialized workers
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(s.TerminationWaitPeriod)
		for _, name := range s.workersInitialized {
			defer func(name string) {
				w := s.workers[name]
				if err := w.Terminate(); err != nil {
					s.logger.
						Error().
						Any("worker", name).
						Msg("Terminated with error")
				}
				s.logger.
					Info().
					Any("worker", name).
					Msg("Worker terminated")
			}(name)
		}
	}()
	waitGroupTimeout(&wg, s.TerminationGracePeriod)
	s.logger.
		Info().
		Msg("All workers terminated")
}

func waitGroupTimeout(wg *sync.WaitGroup, d time.Duration) {
	select {
	case <-waitGroupToChan(wg):
	case <-time.After(d):
	}
}

func waitGroupToChan(wg *sync.WaitGroup) <-chan struct{} {
	c := make(chan struct{})
	go func() {
		defer close(c)
		wg.Wait()
	}()
	return c
}

func (s *SVC) recoverWait(name string, wg *sync.WaitGroup, errors chan<- error) {
	wg.Done()
	if r := recover(); r != nil {
		if err, ok := r.(error); ok {
			s.logger.
				Error().
				Any("worker", name).
				Err(err).
				Msg("recover panic")
			errors <- err
		} else {
			errors <- fmt.Errorf("%v", r)
		}
	}
}
