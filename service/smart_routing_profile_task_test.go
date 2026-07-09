package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
)

func TestSmartRoutingProfileTaskHandlersExposeExpectedSchedule(t *testing.T) {
	external := smartRoutingExternalBenchmarkRefreshHandler{}
	assert.Equal(t, model.SystemTaskTypeSmartRoutingExternalBenchmarkRefresh, external.Type())
	assert.True(t, external.Enabled())
	assert.Equal(t, 10*24*time.Hour, external.Interval())

	profile := smartRoutingModelProfileRefreshHandler{}
	assert.Equal(t, model.SystemTaskTypeSmartRoutingModelProfileRefresh, profile.Type())
	assert.True(t, profile.Enabled())
	assert.Equal(t, 24*time.Hour, profile.Interval())
}
