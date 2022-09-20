package actions

import (
	"time"

	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestNewHealthzProvider(t *testing.T) {
	t.Run("unhealthy statuses", func(t *testing.T) {

		log := logrus.New()
		cfg := Config{
			PollTimeout: 1 * time.Millisecond,
		}

		h := NewHealthzProvider(cfg, log)

		t.Run("should return initialize timeout error", func(t *testing.T) {
			r := require.New(t)
			h.Initializing()

			time.Sleep(5 * time.Millisecond)

			r.Error(h.Check(nil))
		})

		t.Run("should return action pool timeout error", func(t *testing.T) {
			r := require.New(t)
			h.ActionPoll()

			time.Sleep(5 * time.Millisecond)

			r.Error(h.Check(nil))
		})
	})

	t.Run("healthy statuses", func(t *testing.T) {
		log := logrus.New()
		cfg := Config{
			PollTimeout:      1 * time.Second,
			PollWaitInterval: 1 * time.Second,
		}

		h := NewHealthzProvider(cfg, log)

		t.Run("cluster-controller is considered healthy before initialization", func(t *testing.T) {
			r := require.New(t)

			r.NoError(h.Check(nil))
		})

		t.Run("should return no error when still initializing", func(t *testing.T) {
			h.Initializing()
			r := require.New(t)

			r.NoError(h.Check(nil))
		})

		t.Run("should return no error when time since last action pool has not been long", func(t *testing.T) {
			r := require.New(t)
			h.ActionPoll()

			r.NoError(h.Check(nil))
		})
	})
}
