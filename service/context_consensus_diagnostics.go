package service

import (
	"context"
	"errors"
	"sort"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service/contextconsensus"
)

const (
	ContextConsensusDiagnosticsSchemaVersion   = 1
	contextConsensusDiagnosticsMaximumLogBytes = 1 << 20
)

type ContextConsensusDiagnosticsDataQuality struct {
	MatchedLogs        int64 `json:"matched_logs"`
	ValidDiagnostics   int64 `json:"valid_diagnostics"`
	InvalidDiagnostics int64 `json:"invalid_diagnostics"`
	LegacyLogs         int64 `json:"legacy_logs"`
	OversizedLogs      int64 `json:"oversized_logs"`
}

type ContextConsensusDiagnosticsSummary struct {
	NotApplicable        int64   `json:"not_applicable"`
	ToolContexts         int64   `json:"tool_contexts"`
	ReadyForSanitization int64   `json:"ready_for_sanitization"`
	Blocked              int64   `json:"blocked"`
	ReadyRate            float64 `json:"ready_rate"`
	ReasonOccurrences    int64   `json:"reason_occurrences"`
}

type ContextConsensusDiagnosticReasonCount struct {
	ReasonCode string `json:"reason_code"`
	Count      int64  `json:"count"`
}

type ContextConsensusDiagnosticProtocolCount struct {
	Protocol             string `json:"protocol"`
	ToolContexts         int64  `json:"tool_contexts"`
	ReadyForSanitization int64  `json:"ready_for_sanitization"`
	Blocked              int64  `json:"blocked"`
}

type ContextConsensusDiagnosticTimelinePoint struct {
	BucketTimestamp      int64 `json:"bucket_timestamp"`
	ToolContexts         int64 `json:"tool_contexts"`
	ReadyForSanitization int64 `json:"ready_for_sanitization"`
	Blocked              int64 `json:"blocked"`
}

type ContextConsensusDiagnosticsResult struct {
	SchemaVersion  int                                       `json:"schema_version"`
	StartTimestamp int64                                     `json:"start_timestamp"`
	EndTimestamp   int64                                     `json:"end_timestamp"`
	DataScope      string                                    `json:"data_scope"`
	DataQuality    ContextConsensusDiagnosticsDataQuality    `json:"data_quality"`
	Summary        ContextConsensusDiagnosticsSummary        `json:"summary"`
	ByReasonCode   []ContextConsensusDiagnosticReasonCount   `json:"by_reason_code"`
	ByProtocol     []ContextConsensusDiagnosticProtocolCount `json:"by_protocol"`
	Timeline       []ContextConsensusDiagnosticTimelinePoint `json:"timeline"`
}

type contextConsensusDiagnosticsLogEnvelope struct {
	SmartRouting *struct {
		ContextConsensus *struct {
			Protocol                 string                                     `json:"protocol"`
			ToolCompactionDiagnostic *contextconsensus.ToolCompactionDiagnostic `json:"tool_compaction_diagnostic"`
		} `json:"context_consensus"`
	} `json:"smart_routing"`
}

type contextConsensusDiagnosticsAccumulator struct {
	result       ContextConsensusDiagnosticsResult
	reasonCounts map[string]int64
	protocols    map[string]*ContextConsensusDiagnosticProtocolCount
	timeline     map[int64]*ContextConsensusDiagnosticTimelinePoint
}

func QueryContextConsensusDiagnostics(ctx context.Context, startTimestamp, endTimestamp int64) (ContextConsensusDiagnosticsResult, error) {
	accumulator := newContextConsensusDiagnosticsAccumulator(startTimestamp, endTimestamp)
	matched, err := model.IterateSmartRoutingConsumeLogs(ctx, startTimestamp, endTimestamp, SmartRoutingMetricsMaximumLogs, accumulator.add)
	if errors.Is(err, model.ErrSmartRoutingLogQueryLimitExceeded) {
		return ContextConsensusDiagnosticsResult{}, ErrSmartRoutingMetricsTooManyLogs
	}
	if err != nil {
		return ContextConsensusDiagnosticsResult{}, err
	}
	accumulator.result.DataQuality.MatchedLogs = int64(matched)
	return accumulator.finish(), nil
}

func newContextConsensusDiagnosticsAccumulator(startTimestamp, endTimestamp int64) *contextConsensusDiagnosticsAccumulator {
	accumulator := &contextConsensusDiagnosticsAccumulator{
		result: ContextConsensusDiagnosticsResult{
			SchemaVersion:  ContextConsensusDiagnosticsSchemaVersion,
			StartTimestamp: startTimestamp,
			EndTimestamp:   endTimestamp,
			DataScope:      "successful_smart_routing_consume_logs",
		},
		reasonCounts: map[string]int64{},
		protocols:    map[string]*ContextConsensusDiagnosticProtocolCount{},
		timeline:     map[int64]*ContextConsensusDiagnosticTimelinePoint{},
	}
	startBucket := startTimestamp - startTimestamp%3600
	endBucket := endTimestamp - endTimestamp%3600
	for bucket := startBucket; bucket <= endBucket; bucket += 3600 {
		accumulator.timeline[bucket] = &ContextConsensusDiagnosticTimelinePoint{BucketTimestamp: bucket}
	}
	return accumulator
}

func (accumulator *contextConsensusDiagnosticsAccumulator) add(projection model.SmartRoutingLogProjection) error {
	if len(projection.Other) > contextConsensusDiagnosticsMaximumLogBytes {
		accumulator.result.DataQuality.InvalidDiagnostics++
		accumulator.result.DataQuality.OversizedLogs++
		return nil
	}
	var envelope contextConsensusDiagnosticsLogEnvelope
	if err := common.UnmarshalJsonStr(projection.Other, &envelope); err != nil || envelope.SmartRouting == nil || envelope.SmartRouting.ContextConsensus == nil {
		accumulator.result.DataQuality.InvalidDiagnostics++
		return nil
	}
	contextLog := envelope.SmartRouting.ContextConsensus
	if contextLog.ToolCompactionDiagnostic == nil {
		accumulator.result.DataQuality.LegacyLogs++
		return nil
	}
	if !validContextConsensusDiagnosticProtocol(contextLog.Protocol) || contextLog.ToolCompactionDiagnostic.Validate() != nil {
		accumulator.result.DataQuality.InvalidDiagnostics++
		return nil
	}

	accumulator.result.DataQuality.ValidDiagnostics++
	diagnostic := contextLog.ToolCompactionDiagnostic
	if diagnostic.Status == contextconsensus.ToolCompactionDiagnosticNotApplicable {
		accumulator.result.Summary.NotApplicable++
		return nil
	}

	accumulator.result.Summary.ToolContexts++
	protocol := accumulator.protocols[contextLog.Protocol]
	if protocol == nil {
		protocol = &ContextConsensusDiagnosticProtocolCount{Protocol: contextLog.Protocol}
		accumulator.protocols[contextLog.Protocol] = protocol
	}
	protocol.ToolContexts++
	bucket := projection.CreatedAt - projection.CreatedAt%3600
	timelinePoint := accumulator.timeline[bucket]
	if timelinePoint != nil {
		timelinePoint.ToolContexts++
	}

	if diagnostic.Status == contextconsensus.ToolCompactionDiagnosticReadyForSanitization {
		accumulator.result.Summary.ReadyForSanitization++
		protocol.ReadyForSanitization++
		if timelinePoint != nil {
			timelinePoint.ReadyForSanitization++
		}
		return nil
	}

	accumulator.result.Summary.Blocked++
	protocol.Blocked++
	if timelinePoint != nil {
		timelinePoint.Blocked++
	}
	for _, reasonCode := range diagnostic.ReasonCodes {
		accumulator.reasonCounts[reasonCode]++
		accumulator.result.Summary.ReasonOccurrences++
	}
	return nil
}

func validContextConsensusDiagnosticProtocol(protocol string) bool {
	switch types.RelayFormat(protocol) {
	case types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses, types.RelayFormatClaude, types.RelayFormatGemini:
		return true
	default:
		return false
	}
}

func (accumulator *contextConsensusDiagnosticsAccumulator) finish() ContextConsensusDiagnosticsResult {
	if accumulator.result.Summary.ToolContexts > 0 {
		accumulator.result.Summary.ReadyRate = roundSmartRoutingMetric(
			float64(accumulator.result.Summary.ReadyForSanitization) / float64(accumulator.result.Summary.ToolContexts),
		)
	}
	accumulator.result.ByReasonCode = make([]ContextConsensusDiagnosticReasonCount, 0, len(accumulator.reasonCounts))
	for reasonCode, count := range accumulator.reasonCounts {
		accumulator.result.ByReasonCode = append(accumulator.result.ByReasonCode, ContextConsensusDiagnosticReasonCount{ReasonCode: reasonCode, Count: count})
	}
	sort.Slice(accumulator.result.ByReasonCode, func(left, right int) bool {
		if accumulator.result.ByReasonCode[left].Count == accumulator.result.ByReasonCode[right].Count {
			return accumulator.result.ByReasonCode[left].ReasonCode < accumulator.result.ByReasonCode[right].ReasonCode
		}
		return accumulator.result.ByReasonCode[left].Count > accumulator.result.ByReasonCode[right].Count
	})

	protocolNames := make([]string, 0, len(accumulator.protocols))
	for protocol := range accumulator.protocols {
		protocolNames = append(protocolNames, protocol)
	}
	sort.Strings(protocolNames)
	accumulator.result.ByProtocol = make([]ContextConsensusDiagnosticProtocolCount, 0, len(protocolNames))
	for _, protocol := range protocolNames {
		accumulator.result.ByProtocol = append(accumulator.result.ByProtocol, *accumulator.protocols[protocol])
	}

	buckets := make([]int64, 0, len(accumulator.timeline))
	for bucket := range accumulator.timeline {
		buckets = append(buckets, bucket)
	}
	sort.Slice(buckets, func(left, right int) bool { return buckets[left] < buckets[right] })
	accumulator.result.Timeline = make([]ContextConsensusDiagnosticTimelinePoint, 0, len(buckets))
	for _, bucket := range buckets {
		accumulator.result.Timeline = append(accumulator.result.Timeline, *accumulator.timeline[bucket])
	}
	return accumulator.result
}
