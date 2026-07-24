package service

import (
	"errors"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type synchronousRefundFunding struct {
	refundCalls int
	refundErr   error
}

func (funding *synchronousRefundFunding) PreConsume(int) error { return nil }
func (funding *synchronousRefundFunding) Settle(int) error     { return nil }
func (funding *synchronousRefundFunding) Source() string       { return BillingSourceWallet }
func (funding *synchronousRefundFunding) Refund() error {
	funding.refundCalls++
	return funding.refundErr
}

func TestBillingSessionRefundSyncIsIdempotent(t *testing.T) {
	funding := &synchronousRefundFunding{}
	session := &BillingSession{
		relayInfo:        &relaycommon.RelayInfo{IsPlayground: true},
		funding:          funding,
		preConsumedQuota: 12,
		tokenConsumed:    12,
	}
	context, _ := gin.CreateTestContext(httptest.NewRecorder())

	require.NoError(t, session.RefundSync(context))
	require.NoError(t, session.RefundSync(context))
	assert.Equal(t, 1, funding.refundCalls)
	assert.False(t, session.NeedsRefund())
}

func TestBillingSessionRefundSyncRetriesOnlyFailedFundingStep(t *testing.T) {
	funding := &synchronousRefundFunding{refundErr: errors.New("refund failed")}
	session := &BillingSession{
		relayInfo:        &relaycommon.RelayInfo{IsPlayground: true},
		funding:          funding,
		preConsumedQuota: 12,
		tokenConsumed:    12,
	}
	context, _ := gin.CreateTestContext(httptest.NewRecorder())

	require.ErrorContains(t, session.RefundSync(context), "refund failed")
	assert.True(t, session.NeedsRefund())
	funding.refundErr = nil
	require.NoError(t, session.RefundSync(context))
	assert.Equal(t, 2, funding.refundCalls)
	assert.False(t, session.NeedsRefund())
}
