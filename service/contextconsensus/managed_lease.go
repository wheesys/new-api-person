package contextconsensus

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

type ManagedConsensusLeaseGuard struct {
	session  *ManagedConsensusSession
	cancel   context.CancelFunc
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
	mutex    sync.Mutex
	renewErr error
}

// StartManagedConsensusLeaseGuard renews strictly before half of the lease TTL.
// A failed renewal cancels the returned request context and permanently marks
// the session unusable for a later commit.
func StartManagedConsensusLeaseGuard(parent context.Context, session *ManagedConsensusSession, leaseTTL time.Duration) (context.Context, *ManagedConsensusLeaseGuard, error) {
	if parent == nil {
		return nil, nil, fmt.Errorf("managed consensus request context is required")
	}
	if session == nil {
		return nil, nil, fmt.Errorf("managed consensus session is required")
	}
	if leaseTTL <= 0 {
		return nil, nil, fmt.Errorf("managed consensus lease TTL must be positive")
	}
	renewInterval := leaseTTL / 3
	if renewInterval <= 0 || renewInterval >= leaseTTL/2 {
		return nil, nil, fmt.Errorf("managed consensus renewal interval must be less than half the lease TTL")
	}
	ticker := time.NewTicker(renewInterval)
	requestContext, cancel := context.WithCancel(parent)
	guard := startManagedConsensusLeaseGuardWithTicks(requestContext, cancel, session, leaseTTL, ticker.C, ticker.Stop)
	return requestContext, guard, nil
}

func startManagedConsensusLeaseGuardWithTicks(
	requestContext context.Context,
	cancel context.CancelFunc,
	session *ManagedConsensusSession,
	leaseTTL time.Duration,
	ticks <-chan time.Time,
	stopTicks func(),
) *ManagedConsensusLeaseGuard {
	guard := &ManagedConsensusLeaseGuard{
		session: session,
		cancel:  cancel,
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	go func() {
		defer close(guard.done)
		defer stopTicks()
		for {
			select {
			case <-guard.stop:
				return
			case <-requestContext.Done():
				return
			case <-ticks:
				if err := session.Renew(requestContext, leaseTTL); err != nil {
					guard.mutex.Lock()
					guard.renewErr = err
					guard.mutex.Unlock()
					cancel()
					return
				}
			}
		}
	}()
	return guard
}

func (guard *ManagedConsensusLeaseGuard) Session() *ManagedConsensusSession {
	if guard == nil {
		return nil
	}
	return guard.session
}

func (guard *ManagedConsensusLeaseGuard) RenewalError() error {
	if guard == nil {
		return nil
	}
	guard.mutex.Lock()
	defer guard.mutex.Unlock()
	return guard.renewErr
}

// Close always stops renewal before releasing the lease with a short context
// that is independent of client cancellation.
func (guard *ManagedConsensusLeaseGuard) Close(parent context.Context) error {
	if guard == nil {
		return nil
	}
	guard.stopOnce.Do(func() {
		close(guard.stop)
		guard.cancel()
	})
	<-guard.done
	if parent == nil {
		parent = context.Background()
	}
	releaseContext, cancel := context.WithTimeout(context.WithoutCancel(parent), 2*time.Second)
	defer cancel()
	releaseErr := guard.session.Close(releaseContext)
	joined := errors.Join(guard.RenewalError(), releaseErr)
	if joined == nil {
		return nil
	}
	return fmt.Errorf("managed consensus lifecycle failed: %w", joined)
}
