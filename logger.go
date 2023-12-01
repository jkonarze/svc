package svc

import (
	"os"
	"time"

	"github.com/rs/zerolog"
)

const samplingDuration = 1 * time.Second

func (s *SVC) newLogger(name string) *zerolog.Logger {
	logger := zerolog.New(os.Stdout).
		Level(zerolog.InfoLevel).
		With().
		Str("logger", name).
		Timestamp().
		Logger().
		Sample(&zerolog.BurstSampler{
			Burst:  3,
			Period: samplingDuration,
			NextSampler: &zerolog.BasicSampler{
				N: 100,
			},
		})
	return &logger
}
