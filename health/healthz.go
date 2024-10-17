package health

import (
	"fmt"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
)

type HealthzCfg struct {
	// Max time between successful poll actions to consider cluster-controller alive.
	HealthyPollIntervalLimit time.Duration
	// Max wait time for application to start.
	StartTimeLimit time.Duration
}

func NewHealthzProvider(cfg HealthzCfg, log logrus.FieldLogger) *HealthzProvider {
	return &HealthzProvider{
		log: log,
		cfg: cfg,
	}
}

type HealthzProvider struct {
	log logrus.FieldLogger
	cfg HealthzCfg

	lastHealthyActionAt *time.Time
	initStartedAt       *time.Time
}

func (h *HealthzProvider) Check(_ *http.Request) (err error) {
	defer func() {
		if err != nil {
			h.log.Warnf("Health check failed due to: %v", err)
		}
	}()

	if h.lastHealthyActionAt != nil {
		if time.Since(*h.lastHealthyActionAt) > h.cfg.HealthyPollIntervalLimit {
			return fmt.Errorf("time since initialization or last poll action is over the considered healthy limit of %s", h.cfg.HealthyPollIntervalLimit)
		}
		return nil
	}

	if h.initStartedAt != nil {
		if time.Since(*h.initStartedAt) > h.cfg.StartTimeLimit {
			return fmt.Errorf("there was no sucessful poll action since start of application %s", h.cfg.StartTimeLimit)
		}
		return nil
	}

	return nil
}

func (h *HealthzProvider) Name() string {
	return "action-health-check"
}

func (h *HealthzProvider) ActionPoll() {
	h.lastHealthyActionAt = nowPtr()
	h.initStartedAt = nil
}

func (h *HealthzProvider) Initializing() {
	if h.initStartedAt == nil {
		h.initStartedAt = nowPtr()
		h.lastHealthyActionAt = nil
	}
}

func nowPtr() *time.Time {
	now := time.Now()
	return &now
}
