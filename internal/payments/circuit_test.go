package payments

import (
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

func TestCircuitOpensAtThresholdAndRecovers(t *testing.T) {
	c := NewCircuit(2, 5*time.Millisecond)
	assert.True(t, c.Allow())
	c.Failure()
	assert.True(t, c.Allow())
	c.Failure()
	assert.False(t, c.Allow())
	time.Sleep(10 * time.Millisecond)
	assert.True(t, c.Allow())
	c.Success()
	assert.True(t, c.Allow())
}
