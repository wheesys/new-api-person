package service

import (
	"testing"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relaytypes "github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gin-gonic/gin"
)

func TestNewBillingSessionAllowsZeroUserQuotaWhenTokenQuotaIsEnough(t *testing.T) {
	truncate(t)

	const userID = 801
	const tokenID = 801
	const preConsumedQuota = 120

	seedUser(t, userID, 0)
	seedToken(t, tokenID, userID, "billing-zero-wallet", 500)

	c, _ := gin.CreateTestContext(nil)
	c.Set("token_quota", 500)
	relayInfo := &relaycommon.RelayInfo{
		UserId:    userID,
		TokenId:   tokenID,
		TokenKey:  "billing-zero-wallet",
		StartTime: time.Now(),
	}

	session, apiErr := NewBillingSession(c, relayInfo, preConsumedQuota)

	require.Nil(t, apiErr)
	require.NotNil(t, session)
	assert.Equal(t, preConsumedQuota, session.GetPreConsumedQuota())
	assert.Equal(t, 0, getUserQuota(t, userID))
	assert.Equal(t, 500-preConsumedQuota, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, BillingSourceWallet, relayInfo.BillingSource)
}

func TestNewBillingSessionRejectsInsufficientTokenQuotaEvenWithoutUserBalanceCheck(t *testing.T) {
	truncate(t)

	const userID = 802
	const tokenID = 802

	seedUser(t, userID, 0)
	seedToken(t, tokenID, userID, "billing-token-low", 50)

	c, _ := gin.CreateTestContext(nil)
	c.Set("token_quota", 50)
	relayInfo := &relaycommon.RelayInfo{
		UserId:    userID,
		TokenId:   tokenID,
		TokenKey:  "billing-token-low",
		StartTime: time.Now(),
	}

	session, apiErr := NewBillingSession(c, relayInfo, 120)

	require.Nil(t, session)
	require.NotNil(t, apiErr)
	assert.Equal(t, relaytypes.ErrorCodePreConsumeTokenQuotaFailed, apiErr.GetErrorCode())
	assert.Equal(t, 0, getUserQuota(t, userID))
	assert.Equal(t, 50, getTokenRemainQuota(t, tokenID))
}
