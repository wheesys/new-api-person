package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/require"
)

func TestCalcViolationFeeQuotaAppliesChannelRatio(t *testing.T) {
	quota := calcViolationFeeQuota(0.02, 2, 1.5)

	require.Equal(t, int(0.02*common.QuotaPerUnit*2*1.5), quota)
}
