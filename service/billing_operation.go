package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

type durableBillingPricingSnapshot struct {
	PriceData             types.PriceData              `json:"price_data"`
	TieredBillingSnapshot *billingexpr.BillingSnapshot `json:"tiered_billing_snapshot,omitempty"`
}

type durableTextSettlementResult struct {
	Frozen          *model.BillingOperationFrozenResult
	LogRecorded     bool
	SettledNow      bool
	SettlementError error
	LogError        error
}

func billingOperationContext(c *gin.Context) context.Context {
	if c != nil && c.Request != nil {
		return c.Request.Context()
	}
	return context.Background()
}

func (s *BillingSession) hasBillingOperation() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.billingOperationId != 0
}

func (s *BillingSession) reserveBillingOperation(c *gin.Context, preConsumedQuota int) error {
	if err := model.ValidateBillingOperationLogBackend(); err != nil {
		return err
	}
	metadata := s.relayInfo.BillingOperation
	if metadata == nil {
		return fmt.Errorf("billing operation metadata is unavailable")
	}
	candidates := make([]model.BillingOperationLookupCandidate, 0, len(metadata.Candidates))
	for _, candidate := range metadata.Candidates {
		candidates = append(candidates, model.BillingOperationLookupCandidate{
			LookupHMAC:       candidate.LookupHMAC,
			OwnerHMAC:        candidate.OwnerHMAC,
			ConversationHMAC: candidate.ConversationHMAC,
			KeyVersion:       candidate.KeyVersion,
		})
	}
	pricingSnapshot, err := common.Marshal(durableBillingPricingSnapshot{
		PriceData:             s.relayInfo.PriceData,
		TieredBillingSnapshot: s.relayInfo.TieredBillingSnapshot,
	})
	if err != nil {
		return fmt.Errorf("marshal durable billing pricing snapshot: %w", err)
	}
	operation, err := model.ReserveBillingOperation(billingOperationContext(c), model.BillingOperationReserveRequest{
		Identity: model.BillingOperationIdentity{
			Candidates:       candidates,
			ExpectedRevision: metadata.ExpectedRevision,
			Purpose:          metadata.Purpose,
			Fingerprint:      metadata.Fingerprint,
		},
		UserId:          s.relayInfo.UserId,
		TokenId:         s.relayInfo.TokenId,
		ChannelId:       s.relayInfo.ChannelId,
		ReservedQuota:   preConsumedQuota,
		IsPlayground:    s.relayInfo.IsPlayground,
		PricingSnapshot: string(pricingSnapshot),
	})
	if err != nil {
		return err
	}
	var frozenPricing durableBillingPricingSnapshot
	if err := common.Unmarshal([]byte(operation.PricingSnapshot), &frozenPricing); err != nil {
		return fmt.Errorf("decode durable billing pricing snapshot: %w", err)
	}
	if err := model.InvalidateTokenQuotaCache(s.relayInfo.TokenKey); err != nil {
		return fmt.Errorf("invalidate token cache after billing reserve: %w", err)
	}

	s.billingOperationId = operation.Id
	s.billingOperationFingerprint = metadata.Fingerprint
	s.billingOperationState = operation.State
	s.relayInfo.PriceData = frozenPricing.PriceData
	s.relayInfo.TieredBillingSnapshot = frozenPricing.TieredBillingSnapshot
	s.preConsumedQuota = operation.ReservedQuota
	if !operation.IsPlayground {
		s.tokenConsumed = operation.ReservedQuota
	}
	if operation.State == model.BillingOperationStateSettled {
		s.settled = true
		s.fundingSettled = true
	}
	s.relayInfo.BillingOperationId = operation.Id
	s.syncRelayInfo()
	return nil
}

func (s *BillingSession) refundBillingOperation(baseContext context.Context) error {
	s.mu.Lock()
	if s.billingOperationId == 0 || s.billingOperationState == model.BillingOperationStateSettled ||
		s.billingOperationState == model.BillingOperationStateRefunded {
		s.mu.Unlock()
		return nil
	}
	if s.refundInProgress {
		s.mu.Unlock()
		return fmt.Errorf("billing refund is already in progress")
	}
	s.refundInProgress = true
	operationId := s.billingOperationId
	fingerprint := s.billingOperationFingerprint
	tokenKey := s.relayInfo.TokenKey
	s.mu.Unlock()

	refundContext, cancel := context.WithTimeout(context.WithoutCancel(baseContext), 5*time.Second)
	defer cancel()
	operation, err := model.RefundBillingOperation(refundContext, operationId, fingerprint)
	cacheErr := model.InvalidateTokenQuotaCache(tokenKey)

	s.mu.Lock()
	s.refundInProgress = false
	if err == nil {
		s.billingOperationState = operation.State
		if operation.State == model.BillingOperationStateRefunded {
			s.refunded = true
			s.fundingRefunded = true
			s.tokenRefunded = true
		}
	}
	s.mu.Unlock()
	return errors.Join(err, cacheErr)
}

func (s *BillingSession) frozenTextSettlement(c *gin.Context) (*model.BillingOperationFrozenResult, bool, error) {
	s.mu.Lock()
	operationId := s.billingOperationId
	fingerprint := s.billingOperationFingerprint
	s.mu.Unlock()
	if operationId == 0 {
		return nil, false, nil
	}
	frozen, settled, err := model.GetBillingOperationFrozenResult(billingOperationContext(c), operationId, fingerprint)
	if err != nil || !settled {
		return frozen, settled, err
	}
	if !frozen.LogDelivered {
		recorded, deliveryErr := model.DeliverBillingOperationConsumeLog(c, operationId)
		if deliveryErr != nil {
			return frozen, true, deliveryErr
		}
		frozen.LogDelivered = true
		frozen.LogRecorded = recorded
	}
	return frozen, true, nil
}

func (s *BillingSession) settleTextBillingOperation(c *gin.Context, settlement model.BillingOperationSettlement) durableTextSettlementResult {
	s.mu.Lock()
	operationId := s.billingOperationId
	fingerprint := s.billingOperationFingerprint
	tokenKey := s.relayInfo.TokenKey
	s.mu.Unlock()
	if operationId == 0 {
		return durableTextSettlementResult{SettlementError: fmt.Errorf("durable billing operation is unavailable")}
	}

	settlementContext, cancel := context.WithTimeout(context.WithoutCancel(billingOperationContext(c)), 5*time.Second)
	defer cancel()
	operation, settledNow, databaseSettlementErr := model.SettleBillingOperation(settlementContext, operationId, fingerprint, settlement)
	cacheErr := model.InvalidateTokenQuotaCache(tokenKey)
	if operation != nil && operation.State == model.BillingOperationStateSettled {
		s.mu.Lock()
		s.billingOperationState = operation.State
		s.settled = true
		s.fundingSettled = true
		s.mu.Unlock()
	}
	if databaseSettlementErr != nil {
		frozen, settled, lookupErr := model.GetBillingOperationFrozenResult(settlementContext, operationId, fingerprint)
		if lookupErr != nil || !settled {
			return durableTextSettlementResult{SettlementError: errors.Join(databaseSettlementErr, lookupErr, cacheErr)}
		}
		s.mu.Lock()
		s.billingOperationState = model.BillingOperationStateSettled
		s.settled = true
		s.fundingSettled = true
		s.mu.Unlock()
		recorded, logErr := model.DeliverBillingOperationConsumeLog(c, operationId)
		frozen.LogDelivered = logErr == nil
		frozen.LogRecorded = recorded
		return durableTextSettlementResult{
			Frozen: frozen, LogRecorded: recorded, SettlementError: cacheErr, LogError: logErr,
		}
	}
	if cacheErr != nil {
		return durableTextSettlementResult{SettlementError: cacheErr}
	}

	recorded, logErr := model.DeliverBillingOperationConsumeLog(c, operationId)
	frozen, settled, frozenErr := model.GetBillingOperationFrozenResult(settlementContext, operationId, fingerprint)
	if frozenErr != nil {
		databaseSettlementErr = frozenErr
	} else if !settled {
		databaseSettlementErr = fmt.Errorf("billing operation settlement did not reach a terminal state")
	}
	return durableTextSettlementResult{
		Frozen:          frozen,
		LogRecorded:     recorded,
		SettledNow:      settledNow,
		SettlementError: databaseSettlementErr,
		LogError:        logErr,
	}
}
