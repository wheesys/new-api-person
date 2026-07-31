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
	CallID             string `json:"-"`
	FunctionName       string `json:"-"`
	PayloadDigest      string
	OpaqueStatePresent bool
	MatchByName        bool
}

func ValidateToolGraph(events []ToolEvent) (ToolGraph, []ValidationIssue) {
	graph := ToolGraph{
		Exchanges: make([]ToolExchange, 0),
		Groups:    make([]ToolGraphGroup, 0),
	}
	issues := make([]ValidationIssue, 0)
	callIndexByID := make(map[string]int)
	pendingByFunctionName := make(map[string][]int)
	ambiguousFunctionNames := make(map[string]struct{})
	groupIndexByCallContainer := make(map[string]int)
	groupIndexByResultContainer := make(map[string]int)
	failedGroups := make(map[int]struct{})

	for _, event := range events {
		path := fmt.Sprintf("tool_event[%d]", event.Sequence)
		switch event.Kind {
		case ToolEventCall:
			callID := event.CallID
			functionName := event.FunctionName
			identityMode := ToolIdentityModeStableID
			if event.MatchByName {
				identityMode = ToolIdentityModeFunctionName
			}
			if strings.TrimSpace(callID) == "" && !event.MatchByName {
				issues = append(issues, ValidationIssue{Code: "missing_tool_call_id", Path: path, Message: "tool call ID is required"})
				continue
			}
			if event.MatchByName && strings.TrimSpace(functionName) == "" {
				issues = append(issues, ValidationIssue{Code: "missing_tool_function_name", Path: path, Message: "tool function name is required for name-based matching"})
				continue
			}
			if callID != "" {
				if duplicateExchangeIndex, exists := callIndexByID[callID]; exists {
					issues = append(issues, ValidationIssue{Code: "duplicate_tool_call_id", Path: path, Message: "tool call ID must be unique"})
					failedGroups[graph.Exchanges[duplicateExchangeIndex].GroupIndex] = struct{}{}
					continue
				}
			}

			callContainerKey := fmt.Sprintf("%s:%d", event.Protocol, event.ParallelGroup)
			groupIndex, groupExists := groupIndexByCallContainer[callContainerKey]
			if !groupExists {
				groupIndex = len(graph.Groups)
				groupIndexByCallContainer[callContainerKey] = groupIndex
				graph.Groups = append(graph.Groups, ToolGraphGroup{
					Protocol:              event.Protocol,
					CallContainerSequence: event.ParallelGroup,
					CallSequenceStart:     event.Sequence,
					CallSequenceEnd:       event.Sequence,
					ExchangeIndexes:       make([]int, 0),
					Status:                ToolExchangePending,
					IdentityMode:          identityMode,
				})
			} else {
				if graph.Groups[groupIndex].IdentityMode != identityMode {
					issues = append(issues, ValidationIssue{Code: "tool_group_identity_mode_mismatch", Path: path, Message: "tool calls in one group must use the same identity mode"})
					failedGroups[groupIndex] = struct{}{}
					continue
				}
				if event.Sequence < graph.Groups[groupIndex].CallSequenceStart {
					graph.Groups[groupIndex].CallSequenceStart = event.Sequence
				}
				if event.Sequence > graph.Groups[groupIndex].CallSequenceEnd {
					graph.Groups[groupIndex].CallSequenceEnd = event.Sequence
				}
			}

			exchangeIndex := len(graph.Exchanges)
			graph.Exchanges = append(graph.Exchanges, ToolExchange{
				Protocol:           event.Protocol,
				Sequence:           event.Sequence,
				GroupIndex:         groupIndex,
				CallID:             callID,
				FunctionName:       functionName,
				ArgumentsDigest:    event.PayloadDigest,
				Status:             ToolExchangePending,
				RawCallPresent:     true,
				OpaqueStatePresent: event.OpaqueStatePresent,
			})
			graph.Groups[groupIndex].ExchangeIndexes = append(graph.Groups[groupIndex].ExchangeIndexes, exchangeIndex)
			if callID != "" {
				callIndexByID[callID] = exchangeIndex
			}
			if event.MatchByName {
				pendingByFunctionName[functionName] = append(pendingByFunctionName[functionName], exchangeIndex)
				if len(pendingByFunctionName[functionName]) > 1 {
					ambiguousFunctionNames[functionName] = struct{}{}
					for _, ambiguousExchangeIndex := range pendingByFunctionName[functionName] {
						failedGroups[graph.Exchanges[ambiguousExchangeIndex].GroupIndex] = struct{}{}
					}
				}
			}

		case ToolEventResult:
			exchangeIndex, found := callIndexByID[event.CallID]
			matchedFunctionName := ""
			if event.MatchByName {
				matchedFunctionName = event.FunctionName
				pending := pendingByFunctionName[matchedFunctionName]
				if len(pending) > 1 {
					issues = append(issues, ValidationIssue{Code: "ambiguous_tool_result", Path: path, Message: "tool result cannot be assigned to one of multiple same-name calls"})
					for _, ambiguousExchangeIndex := range pending {
						failedGroups[graph.Exchanges[ambiguousExchangeIndex].GroupIndex] = struct{}{}
					}
					continue
				}
				if len(pending) == 1 {
					exchangeIndex = pending[0]
					found = true
				}
			}
			if !found {
				issues = append(issues, ValidationIssue{Code: "orphan_tool_result", Path: path, Message: "tool result has no matching call"})
				continue
			}
			groupIndex := graph.Exchanges[exchangeIndex].GroupIndex
			if graph.Exchanges[exchangeIndex].RawResultPresent {
				issues = append(issues, ValidationIssue{Code: "duplicate_tool_result", Path: path, Message: "tool call has more than one result"})
				failedGroups[groupIndex] = struct{}{}
				continue
			}
			if event.Protocol != graph.Exchanges[exchangeIndex].Protocol {
				issues = append(issues, ValidationIssue{Code: "tool_result_protocol_mismatch", Path: path, Message: "tool result protocol does not match its call"})
				failedGroups[groupIndex] = struct{}{}
				continue
			}
			if event.Sequence <= graph.Exchanges[exchangeIndex].Sequence {
				issues = append(issues, ValidationIssue{Code: "tool_result_order_invalid", Path: path, Message: "tool result must occur after its call"})
				failedGroups[groupIndex] = struct{}{}
				continue
			}
			resultContainerKey := fmt.Sprintf("%s:%d", event.Protocol, event.ParallelGroup)
			if inheritedGroupIndex, exists := groupIndexByResultContainer[resultContainerKey]; exists && inheritedGroupIndex != groupIndex {
				issues = append(issues, ValidationIssue{Code: "tool_result_group_mismatch", Path: path, Message: "one result container cannot close calls from different call groups"})
				failedGroups[inheritedGroupIndex] = struct{}{}
				failedGroups[groupIndex] = struct{}{}
				continue
			}
			groupIndexByResultContainer[resultContainerKey] = groupIndex
			if event.MatchByName {
				pendingByFunctionName[matchedFunctionName] = pendingByFunctionName[matchedFunctionName][1:]
			}
			resultSequence := event.Sequence
			graph.Exchanges[exchangeIndex].ResultDigest = event.PayloadDigest
			graph.Exchanges[exchangeIndex].ResultSequence = &resultSequence
			graph.Exchanges[exchangeIndex].RawResultPresent = true
			graph.Exchanges[exchangeIndex].Status = ToolExchangeCompleted
			graph.Exchanges[exchangeIndex].OpaqueStatePresent = graph.Exchanges[exchangeIndex].OpaqueStatePresent || event.OpaqueStatePresent
			if graph.Groups[groupIndex].ResultSequenceStart == nil || event.Sequence < *graph.Groups[groupIndex].ResultSequenceStart {
				resultSequenceStart := event.Sequence
				graph.Groups[groupIndex].ResultSequenceStart = &resultSequenceStart
			}
			if graph.Groups[groupIndex].ResultSequenceEnd == nil || event.Sequence > *graph.Groups[groupIndex].ResultSequenceEnd {
				resultSequenceEnd := event.Sequence
				graph.Groups[groupIndex].ResultSequenceEnd = &resultSequenceEnd
			}

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
	for groupIndex := range graph.Groups {
		sort.SliceStable(graph.Groups[groupIndex].ExchangeIndexes, func(leftIndex, rightIndex int) bool {
			leftExchange := graph.Exchanges[graph.Groups[groupIndex].ExchangeIndexes[leftIndex]]
			rightExchange := graph.Exchanges[graph.Groups[groupIndex].ExchangeIndexes[rightIndex]]
			if leftExchange.Sequence != rightExchange.Sequence {
				return leftExchange.Sequence < rightExchange.Sequence
			}
			return graph.Groups[groupIndex].ExchangeIndexes[leftIndex] < graph.Groups[groupIndex].ExchangeIndexes[rightIndex]
		})
		if _, failed := failedGroups[groupIndex]; failed {
			graph.Groups[groupIndex].Status = ToolExchangeFailed
			continue
		}
		graph.Groups[groupIndex].Status = ToolExchangeCompleted
		for _, exchangeIndex := range graph.Groups[groupIndex].ExchangeIndexes {
			if graph.Exchanges[exchangeIndex].Status != ToolExchangeCompleted {
				graph.Groups[groupIndex].Status = ToolExchangePending
				break
			}
		}
	}
	for functionName := range ambiguousFunctionNames {
		graph.AmbiguousFunctionNames = append(graph.AmbiguousFunctionNames, functionName)
	}
	sort.Strings(graph.AmbiguousFunctionNames)
	return graph, issues
}
