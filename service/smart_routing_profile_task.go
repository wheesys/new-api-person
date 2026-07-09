package service

import (
	"context"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/smartrouting"
)

type smartRoutingExternalBenchmarkRefreshHandler struct{}
type smartRoutingModelProfileRefreshHandler struct{}

func RegisterSmartRoutingProfileTasks() {
	RegisterSystemTaskHandler(smartRoutingExternalBenchmarkRefreshHandler{})
	RegisterSystemTaskHandler(smartRoutingModelProfileRefreshHandler{})
}

func (smartRoutingExternalBenchmarkRefreshHandler) Type() string {
	return model.SystemTaskTypeSmartRoutingExternalBenchmarkRefresh
}

func (smartRoutingExternalBenchmarkRefreshHandler) Enabled() bool { return true }

func (smartRoutingExternalBenchmarkRefreshHandler) Interval() time.Duration {
	return smartrouting.ExternalBenchmarkRefreshInterval()
}

func (smartRoutingExternalBenchmarkRefreshHandler) NewPayload() any { return nil }

func (smartRoutingExternalBenchmarkRefreshHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	result, err := smartrouting.RefreshExternalBenchmarks(ctx)
	finishSmartRoutingProfileTask(task, runnerID, result, err)
}

func (smartRoutingModelProfileRefreshHandler) Type() string {
	return model.SystemTaskTypeSmartRoutingModelProfileRefresh
}

func (smartRoutingModelProfileRefreshHandler) Enabled() bool { return true }

func (smartRoutingModelProfileRefreshHandler) Interval() time.Duration {
	return smartrouting.ModelProfileRefreshInterval()
}

func (smartRoutingModelProfileRefreshHandler) NewPayload() any { return nil }

func (smartRoutingModelProfileRefreshHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	result, err := smartrouting.RefreshModelRoutingProfiles(ctx)
	finishSmartRoutingProfileTask(task, runnerID, result, err)
}

func finishSmartRoutingProfileTask(task *model.SystemTask, runnerID string, result any, err error) {
	status := model.SystemTaskStatusSucceeded
	errorMessage := ""
	if err != nil {
		status = model.SystemTaskStatusFailed
		errorMessage = err.Error()
	}
	_ = model.FinishSystemTask(task.TaskID, runnerID, status, result, errorMessage)
}
