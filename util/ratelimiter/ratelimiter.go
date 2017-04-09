package ratelimiter

import (
	"time"

	"github.com/adohe/kube2haproxy/util/flowcontrol"

	kcache "k8s.io/kubernetes/pkg/client/cache"
	utilruntime "k8s.io/kubernetes/pkg/util/runtime"
	utilwait "k8s.io/kubernetes/pkg/util/wait"
)

const maxBackOffPeriod time.Duration = 30 * time.Second

// HandlerFunc defines function signature for a RateLimitedFunction.
type HandlerFunc func() error

// RateLimitedFunction is a rate limited function controlling how often the function/handler is invoked.
type RateLimitedFunction struct {
	// Handler is the function to rate limit calls to.
	Handler HandlerFunc

	// Internal queue of requests to be processed.
	queue kcache.Queue

	// Backoff configuration
	backOffKey string
	backOff    *flowcontrol.Backoff
}

// NewRateLimitedFunction creates a new rate limited function.
func NewRateLimitedFunction(backOffKey string, period time.Duration, handlerFunc HandlerFunc) *RateLimitedFunction {
	keyFunc := func(_ interface{}) (string, error) {
		return backOffKey, nil
	}
	fifo := kcache.NewFIFO(keyFunc)

	backOff := flowcontrol.NewBackOff(period, maxBackOffPeriod)

	return &RateLimitedFunction{handlerFunc, fifo, backOffKey, backOff}
}

// RunUntil begins processes the resources from queue asynchronously until
// stopCh is closed.
func (rlf *RateLimitedFunction) RunUntil(stopCh <-chan struct{}) {
	go utilwait.Until(func() { rlf.handleOne(rlf.queue.Pop()) }, 0, stopCh)
}

// handleOne processes a request in the queue invoking the rate limited
// function.
func (rlf *RateLimitedFunction) handleOne(resource interface{}) {
	if rlf.backOff.IsInBackOffSinceUpdate(rlf.backOffKey, rlf.backOff.Clock.Now()) {
		rlf.queue.AddIfNotPresent(resource)
		return
	}
	if err := rlf.Handler(); err != nil {
		utilruntime.HandleError(err)
	}
	rlf.backOff.Next(rlf.backOffKey, rlf.backOff.Clock.Now())
}

// Invoke adds a request if its not already present and waits for the
// background processor to execute it.
func (rlf *RateLimitedFunction) Invoke(resource interface{}) {
	rlf.queue.AddIfNotPresent(resource)
}
