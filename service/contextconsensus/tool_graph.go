package contextconsensus

import (
	"fmt"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/relaykit/types"
)

type ToolEventKind string

const (
	ToolEventCall   ToolEventKind = "call"
	ToolEventResult ToolEventKind = "result"
)

type ToolEvent struct {
	Kind               ToolEventKind
	Protocol           types.RelayFormat
	Sequence           int
	ParallelGroup      int
	CallID             string
	FunctionName       string
	PayloadDigest      string
	OpaqueStatePresent bool
	MatchByName        bool
}

func ValidateToolGraph(events []ToolEvent) (ToolGraph, []ValidationIssue) {
	graph := ToolGraph{Exchanges: make([]ToolExchange, 0)}
	issues := make([]ValidationIssue, 0)
	callIndexByID := make(map[string]int)
	pendingByFunctionName := make(map[string][]int)
	ambiguousFunctionNames := make(map[string]struct{})

	for _, event := range events {
		path := fmt.Sprintf("tool_event[%d]", event.Sequence)
		switch event.Kind {
		case ToolEventCall:
			callID := strings.TrimSpace(event.CallID)
			functionName := strings.TrimSpace(event.FunctionName)
			if callID == "" && !event.MatchByName {
				issues = append(issues, ValidationIssue{Code: "missing_tool_call_id", Path: path, Message: "tool call ID is required"})
				continue
			}
			if callID != "" {
				if _, exists := callIndexByID[callID]; exists {
					issues = append(issues, ValidationIssue{Code: "duplicate_tool_call_id", Path: path, Message: "tool call ID must be unique"})
					continue
				}
			}

			exchangeIndex := len(graph.Exchanges)
			graph.Exchanges = append(graph.Exchanges, ToolExchange{
				Protocol:           event.Protocol,
				Sequence:           event.Sequence,
				ParallelGroup:      event.ParallelGroup,
				CallID:             callID,
				FunctionName:       functionName,
				ArgumentsDigest:    event.PayloadDigest,
				Status:             ToolExchangePending,
				RawCallPresent:     true,
				OpaqueStatePresent: event.OpaqueStatePresent,
			})
			if callID != "" {
				callIndexByID[callID] = exchangeIndex
			}
			if event.MatchByName {
				pendingByFunctionName[functionName] = append(pendingByFunctionName[functionName], exchangeIndex)
				if len(pendingByFunctionName[functionName]) > 1 {
					ambiguousFunctionNames[functionName] = struct{}{}
				}
			}

		case ToolEventResult:
			exchangeIndex, found := callIndexByID[strings.TrimSpace(event.CallID)]
			if event.MatchByName {
				pending := pendingByFunctionName[strings.TrimSpace(event.FunctionName)]
				if len(pending) > 0 {
					exchangeIndex = pending[0]
					pendingByFunctionName[strings.TrimSpace(event.FunctionName)] = pending[1:]
					found = true
				}
			}
			if !found {
				issues = append(issues, ValidationIssue{Code: "orphan_tool_result", Path: path, Message: "tool result has no matching call"})
				continue
			}
			if graph.Exchanges[exchangeIndex].RawResultPresent {
				issues = append(issues, ValidationIssue{Code: "duplicate_tool_result", Path: path, Message: "tool call has more than one result"})
				continue
			}
			graph.Exchanges[exchangeIndex].ResultDigest = event.PayloadDigest
			graph.Exchanges[exchangeIndex].RawResultPresent = true
			graph.Exchanges[exchangeIndex].Status = ToolExchangeCompleted
			graph.Exchanges[exchangeIndex].OpaqueStatePresent = graph.Exchanges[exchangeIndex].OpaqueStatePresent || event.OpaqueStatePresent

		default:
			issues = append(issues, ValidationIssue{Code: "unknown_tool_event", Path: path, Message: "tool event kind is not supported"})
		}
	}

	for index := range graph.Exchanges {
		if graph.Exchanges[index].Status == ToolExchangePending {
			issues = append(issues, ValidationIssue{
				Code:    "missing_tool_result",
				Path:    fmt.Sprintf("tool_exchange[%d]", index),
				Message: "tool call is not closed by a result",
			})
		}
	}
	for functionName := range ambiguousFunctionNames {
		graph.AmbiguousFunctionNames = append(graph.AmbiguousFunctionNames, functionName)
	}
	sort.Strings(graph.AmbiguousFunctionNames)
	return graph, issues
}
