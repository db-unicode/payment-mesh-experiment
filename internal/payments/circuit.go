package payments

import (
	"errors"
	"sync"
	"time"
)

var ErrCircuitOpen = errors.New("gateway circuit open")

type Circuit struct { mu sync.Mutex; failures int; openedAt time.Time; threshold int; cooldown time.Duration }

func NewCircuit(threshold int, cooldown time.Duration) *Circuit { return &Circuit{threshold: threshold, cooldown: cooldown} }
func (c *Circuit) Allow() bool {
	c.mu.Lock(); defer c.mu.Unlock()
	if c.openedAt.IsZero() { return true }
	if time.Since(c.openedAt) >= c.cooldown { c.openedAt = time.Time{}; c.failures = 0; return true }
	return false
}
func (c *Circuit) Success() { c.mu.Lock(); c.failures = 0; c.openedAt = time.Time{}; c.mu.Unlock() }
func (c *Circuit) Failure() { c.mu.Lock(); defer c.mu.Unlock(); c.failures++; if c.failures >= c.threshold { c.openedAt = time.Now() } }
