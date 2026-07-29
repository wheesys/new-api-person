package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service/contextconsensus"
	"github.com/QuantumNous/new-api/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildManagedBillingOperationIdentityIsStableAndBindsAccountingInputs(t *testing.T) {
	seed := managedBillingOperationSeed{
		Candidates: []contextconsensus.ManagedBillingOperationLookupCandidate{{
			LookupHMAC: "lookup-v2", OwnerHMAC: "owner-v2", ConversationHMAC: "conversation-v2", KeyVersion: "v2",
		}},
		ExpectedRevision: 4,
		Purpose:          managedBillingPurposeMain,
		Protocol:         types.RelayFormatOpenAI,
		SourceDigest:     "source-digest",
		PolicyVersion:    "policy-v1",
	}
	info := &relaycommon.RelayInfo{
		OriginModelName:     "gpt-origin",
		FinalRequestModel:   "gpt-final",
		ChannelMeta:         &relaycommon.ChannelMeta{ChannelId: 11, ChannelType: 1},
		BillingRequestInput: &billingexpr.RequestInput{Body: []byte(`{"messages":[{"role":"user","content":"hello"}]}`)},
		PriceData: types.PriceData{
			ModelRatio: 1, CompletionRatio: 2, ChannelRatio: 1, ChannelRatioSet: true,
			QuotaToPreConsume: 50, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
		},
	}

	first, err := buildManagedBillingOperationIdentity(seed, info)
	require.NoError(t, err)
	second, err := buildManagedBillingOperationIdentity(seed, info)
	require.NoError(t, err)
	assert.Equal(t, first, second)
	require.Len(t, first.Candidates, 1)
	assert.Equal(t, "lookup-v2", first.Candidates[0].LookupHMAC)

	changedChannel := *info
	changedChannel.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: 12, ChannelType: 1}
	channelIdentity, err := buildManagedBillingOperationIdentity(seed, &changedChannel)
	require.NoError(t, err)
	assert.NotEqual(t, first.Fingerprint, channelIdentity.Fingerprint)

	changedSeed := seed
	changedSeed.Purpose = managedBillingPurposeSummary
	summaryIdentity, err := buildManagedBillingOperationIdentity(changedSeed, info)
	require.NoError(t, err)
	assert.NotEqual(t, first.Fingerprint, summaryIdentity.Fingerprint)

	changedRequest := *info
	changedRequest.BillingRequestInput = &billingexpr.RequestInput{Body: []byte(`{"messages":[{"role":"user","content":"changed"}]}`)}
	requestIdentity, err := buildManagedBillingOperationIdentity(seed, &changedRequest)
	require.NoError(t, err)
	assert.NotEqual(t, first.Fingerprint, requestIdentity.Fingerprint)

	changedPrice := *info
	changedPrice.PriceData.ModelRatio = 3
	priceIdentity, err := buildManagedBillingOperationIdentity(seed, &changedPrice)
	require.NoError(t, err)
	assert.Equal(t, first.Fingerprint, priceIdentity.Fingerprint, "a retry must reuse the persisted pricing snapshot")
}

func TestBuildManagedBillingOperationIdentityRejectsIncompleteSeed(t *testing.T) {
	_, err := buildManagedBillingOperationIdentity(managedBillingOperationSeed{}, &relaycommon.RelayInfo{})
	assert.Error(t, err)
}
