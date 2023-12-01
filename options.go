package svc

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/pprof"
	"time"
)

// Option defines SVC's option type.
type Option func(*SVC) error

// WithTerminationWaitPeriod is an option that sets the termination wait period.
func WithTerminationWaitPeriod(d time.Duration) Option {
	return func(s *SVC) error {
		s.TerminationWaitPeriod = d

		return nil
	}
}

// WithTerminationGracePeriod is an option that sets the termination grace period.
func WithTerminationGracePeriod(d time.Duration) Option {
	return func(s *SVC) error {
		s.TerminationGracePeriod = d

		return nil
	}
}

// WithRouter is an option that replaces the HTTP router with the given http
// router.
func WithRouter(router *http.ServeMux) Option {
	return func(s *SVC) error {
		s.Router = router
		return nil
	}
}

// WithPProfHandlers is an option that exposes Go's Performance Profiler via
// HTTP routes.
func WithPProfHandlers() Option {
	return func(s *SVC) error {
		// See https://github.com/golang/go/blob/master/src/net/http/pprof/pprof.go#L72-L77
		s.Router.HandleFunc("/debug/pprof/", pprof.Index)
		s.Router.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		s.Router.HandleFunc("/debug/pprof/profile", pprof.Profile)
		s.Router.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		s.Router.HandleFunc("/debug/pprof/trace", pprof.Trace)
		// See https://github.com/golang/go/blob/master/src/net/http/pprof/pprof.go#L248-L258
		s.Router.Handle("/debug/pprof/allocs", pprof.Handler("allocs"))
		s.Router.Handle("/debug/pprof/block", pprof.Handler("block"))
		s.Router.Handle("/debug/pprof/goroutine", pprof.Handler("goroutine"))
		s.Router.Handle("/debug/pprof/heap", pprof.Handler("heap"))
		s.Router.Handle("/debug/pprof/mutex", pprof.Handler("mutex"))
		s.Router.Handle("/debug/pprof/threadcreate", pprof.Handler("threadcreate"))

		return nil
	}
}

// WithHealthz is an option that exposes Kubernetes conform Healthz HTTP
// routes.
func WithHealthz() Option {
	return func(s *SVC) error {
		// Register live probe handler
		s.Router.HandleFunc("/live", func(w http.ResponseWriter, r *http.Request) {
			var errs []error
			for n, w := range s.workers {
				if hw, ok := w.(Aliver); ok {
					if err := hw.Alive(); err != nil {
						errs = append(errs, fmt.Errorf("worker %s: %s", n, err))
					}
				}
			}
			if len(errs) == 0 {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"status": "Still Alive!"}`))
				return
			}

			s.logger.
				Warn().
				Any("errors", errs).
				Msg("liveliness probe failed")
			b, err := json.Marshal(map[string]interface{}{"errors": errs})
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write(b)
		})

		// Register ready probe handler
		s.Router.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
			var errs []error
			for n, w := range s.workers {
				if hw, ok := w.(Healther); ok {
					if err := hw.Healthy(); err != nil {
						errs = append(errs, fmt.Errorf("worker %s: %s", n, err))
					}
				}
			}
			if len(errs) > 0 {
				s.logger.
					Warn().
					Any("errors", errs).
					Msg("Ready check failed")
				b, err := json.Marshal(map[string]interface{}{"errors": errs})
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusServiceUnavailable)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(b)
			}
		})

		return nil
	}
}
