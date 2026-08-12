package service

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPickChannelSkippingOpen(t *testing.T) {
	healthy := &model.Channel{Id: 1}
	open := &model.Channel{Id: 2}

	restorePick := randomSatisfiedChannel
	restoreHealth := runtimeHealthOpen
	defer func() {
		randomSatisfiedChannel = restorePick
		runtimeHealthOpen = restoreHealth
	}()

	t.Run("returns healthy channel immediately", func(t *testing.T) {
		randomSatisfiedChannel = func(string, string, int, string) (*model.Channel, error) {
			return healthy, nil
		}
		runtimeHealthOpen = func(int, string) bool { return false }

		ch, err := pickChannelSkippingOpen("g", "m", 0, "")
		require.NoError(t, err)
		assert.Equal(t, 1, ch.Id)
	})

	t.Run("skips open channel and picks the next", func(t *testing.T) {
		picks := 0
		randomSatisfiedChannel = func(string, string, int, string) (*model.Channel, error) {
			picks++
			if picks == 1 {
				return open, nil
			}
			return healthy, nil
		}
		runtimeHealthOpen = func(id int, _ string) bool {
			return id == open.Id
		}

		ch, err := pickChannelSkippingOpen("g", "m", 0, "")
		require.NoError(t, err)
		assert.Equal(t, 1, ch.Id)
		assert.Equal(t, 2, picks)
	})

	t.Run("propagates model error", func(t *testing.T) {
		randomSatisfiedChannel = func(string, string, int, string) (*model.Channel, error) {
			return nil, errors.New("no channel")
		}
		ch, err := pickChannelSkippingOpen("g", "m", 0, "")
		require.Error(t, err)
		assert.Nil(t, ch)
	})

	t.Run("returns nil when no channel found", func(t *testing.T) {
		randomSatisfiedChannel = func(string, string, int, string) (*model.Channel, error) {
			return nil, nil
		}
		ch, err := pickChannelSkippingOpen("g", "m", 0, "")
		require.NoError(t, err)
		assert.Nil(t, ch)
	})

	t.Run("falls back to last pick when all open", func(t *testing.T) {
		randomSatisfiedChannel = func(string, string, int, string) (*model.Channel, error) {
			return open, nil
		}
		runtimeHealthOpen = func(id int, _ string) bool { return id == open.Id }

		ch, err := pickChannelSkippingOpen("g", "m", 0, "")
		require.NoError(t, err)
		// Bounded attempts exhausted, fall back to the last open pick.
		assert.Equal(t, 2, ch.Id)
	})
}
