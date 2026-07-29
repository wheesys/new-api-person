package controller

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service/contextconsensus"
)

const (
	managedBillingPurposeMain    = "main"
	managedBillingPurposeSummary = "summary"
)

type managedBillingOperationSeed struct {
	Candidates       []contextconsensus.ManagedBillingOperationLookupCandidate
	ExpectedRevision uint64
	Purpose          string
	Protocol         types.RelayFormat
	SourceDigest     string
	PolicyVersion    string
}

func buildManagedBillingOperationIdentity(seed managedBillingOperationSeed, info *relaycommon.RelayInfo) (*relaycommon.BillingOperationIdentity, error) {
	if len(seed.Candidates) == 0 || info == nil || info.ChannelMeta == nil || strings.TrimSpace(seed.SourceDigest) == "" ||
		strings.TrimSpace(seed.PolicyVersion) == "" || seed.Protocol == "" || strings.TrimSpace(seed.Purpose) == "" {
		return nil, fmt.Errorf("managed billing operation seed is incomplete")
	}
	fingerprintInput := struct {
		ExpectedRevision   uint64            `json:"expected_revision"`
		Purpose            string            `json:"purpose"`
		Protocol           types.RelayFormat `json:"protocol"`
		SourceDigest       string            `json:"source_digest"`
		PolicyVersion      string            `json:"policy_version"`
		OriginModel        string            `json:"origin_model"`
		FinalModel         string            `json:"final_model"`
		ChannelID          int               `json:"channel_id"`
		ChannelType        int               `json:"channel_type"`
		BillingInputDigest string            `json:"billing_input_digest,omitempty"`
	}{
		ExpectedRevision: seed.ExpectedRevision,
		Purpose:          seed.Purpose,
		Protocol:         seed.Protocol,
		SourceDigest:     seed.SourceDigest,
		PolicyVersion:    seed.PolicyVersion,
		OriginModel:      info.OriginModelName,
		FinalModel:       info.FinalRequestModel,
		ChannelID:        info.ChannelId,
		ChannelType:      info.ChannelType,
	}
	if info.BillingRequestInput != nil {
		billingInput, err := common.Marshal(info.BillingRequestInput)
		if err != nil {
			return nil, fmt.Errorf("encode managed billing request input: %w", err)
		}
		fingerprintInput.BillingInputDigest = hex.EncodeToString(common.Sha256Raw(billingInput))
	}
	encoded, err := common.Marshal(fingerprintInput)
	if err != nil {
		return nil, fmt.Errorf("encode managed billing fingerprint: %w", err)
	}
	candidates := make([]relaycommon.BillingOperationLookupCandidate, 0, len(seed.Candidates))
	for _, candidate := range seed.Candidates {
		candidates = append(candidates, relaycommon.BillingOperationLookupCandidate{
			LookupHMAC:       candidate.LookupHMAC,
			OwnerHMAC:        candidate.OwnerHMAC,
			ConversationHMAC: candidate.ConversationHMAC,
			KeyVersion:       candidate.KeyVersion,
		})
	}
	return &relaycommon.BillingOperationIdentity{
		Candidates:       candidates,
		ExpectedRevision: seed.ExpectedRevision,
		Purpose:          seed.Purpose,
		Fingerprint:      hex.EncodeToString(common.Sha256Raw(encoded)),
	}, nil
}
