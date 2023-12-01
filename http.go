package svc

import (
	"context"
	"errors"
	"github.com/rs/zerolog"
	"log"
	"net"
	"net/http"
	"time"
)

var _ Worker = (*httpServer)(nil)

// httpServer defines the internal HTTP Server worker.
type httpServer struct {
	logger     *zerolog.Logger
	addr       string
	httpServer *http.Server
}

func newHTTPServer(port string, handler http.Handler, logger *log.Logger) *httpServer {
	addr := net.JoinHostPort("", port)
	return &httpServer{
		addr: addr,
		httpServer: &http.Server{
			Addr:              addr,
			Handler:           handler,
			ErrorLog:          logger,
			ReadHeaderTimeout: 5 * time.Second, // https://medium.com/a-journey-with-go/go-understand-and-mitigate-slowloris-attack-711c1b1403f6
		},
	}
}

// Init implements the Worker interface.
func (s *httpServer) Init(logger *zerolog.Logger) error {
	s.logger = logger

	return nil
}

// Healthy implements the Healther interface.
func (s *httpServer) Healthy() error {
	return nil
}

// Run implements the Worker interface.
func (s *httpServer) Run() error {
	s.logger.
		Info().
		Any("address", s.addr).
		Msg("Listening and serving HTTP")
	if err := s.httpServer.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		s.logger.
			Error().
			Err(err).
			Msg("Failed to serve HTTP")
	}
	return nil
}

// Terminate implements the Worker interface.
func (s *httpServer) Terminate() error {
	return s.httpServer.Shutdown(context.Background())
}
