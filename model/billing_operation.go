package model

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	BillingOperationStateReserved = "reserved"
	BillingOperationStateSettled  = "settled"
	BillingOperationStateRefunded = "refunded"
)

var (
	ErrBillingOperationFingerprintConflict = errors.New("billing operation fingerprint conflict")
	ErrBillingOperationLookupConflict      = errors.New("billing operation lookup conflict")
	ErrBillingOperationRefunded            = errors.New("billing operation has already been refunded")
	ErrBillingOperationQuotaInsufficient   = errors.New("billing operation token quota is insufficient")
)

// BillingOperation is the durable accounting authority for one managed request purpose.
type BillingOperation struct {
	Id               int64      `json:"id" gorm:"primaryKey;autoIncrement;comment:Billing operation numeric identifier"`
	LookupHMAC       string     `json:"-" gorm:"type:varchar(64);not null;index;comment:Versioned HMAC used to locate this operation"`
	OwnerHMAC        string     `json:"-" gorm:"type:varchar(64);not null;uniqueIndex:idx_billing_operation_business,priority:1;comment:HMAC identifying the operation owner"`
	ConversationHMAC string     `json:"-" gorm:"type:varchar(64);not null;uniqueIndex:idx_billing_operation_business,priority:2;comment:HMAC identifying the managed conversation"`
	LookupKeyVersion string     `json:"-" gorm:"type:varchar(64);not null;index;comment:Key version used to derive lookup HMAC values"`
	ExpectedRevision uint64     `json:"expected_revision" gorm:"not null;uniqueIndex:idx_billing_operation_business,priority:3;comment:Conversation revision consumed by this operation"`
	Purpose          string     `json:"purpose" gorm:"type:varchar(32);not null;uniqueIndex:idx_billing_operation_business,priority:4;comment:Stable purpose separating main and child operations"`
	Fingerprint      string     `json:"-" gorm:"type:varchar(64);not null;comment:Fingerprint binding immutable operation inputs"`
	State            string     `json:"state" gorm:"type:varchar(24);not null;index;comment:Durable operation lifecycle state"`
	UserId           int        `json:"user_id" gorm:"not null;index;comment:User charged by this operation"`
	TokenId          int        `json:"token_id" gorm:"not null;index;comment:API token charged by this operation"`
	ChannelId        int        `json:"channel_id" gorm:"not null;comment:Upstream channel accounted by settlement"`
	ReservedQuota    int        `json:"reserved_quota" gorm:"not null;comment:Quota reserved before the upstream call"`
	ActualQuota      int        `json:"actual_quota" gorm:"not null;comment:Frozen quota charged at settlement"`
	PromptTokens     int        `json:"prompt_tokens" gorm:"not null;comment:Frozen prompt token count"`
	CompletionTokens int        `json:"completion_tokens" gorm:"not null;comment:Frozen completion token count"`
	TotalTokens      int        `json:"total_tokens" gorm:"not null;comment:Frozen total token count"`
	BillingMode      string     `json:"billing_mode" gorm:"type:varchar(32);not null;comment:Frozen billing mode used for settlement"`
	PricingSnapshot  string     `json:"-" gorm:"type:text;not null;comment:Frozen pre-consume pricing snapshot encoded as JSON"`
	CountUsage       bool       `json:"count_usage" gorm:"not null;comment:Whether user and channel usage counters were updated"`
	IsPlayground     bool       `json:"is_playground" gorm:"not null;comment:Whether token quota accounting is bypassed"`
	CreatedAt        time.Time  `json:"created_at" gorm:"not null;comment:Timestamp when the operation was reserved"`
	UpdatedAt        time.Time  `json:"updated_at" gorm:"not null;comment:Timestamp when the operation last changed"`
	SettledAt        *time.Time `json:"settled_at,omitempty" gorm:"comment:Timestamp when settlement committed"`
	RefundedAt       *time.Time `json:"refunded_at,omitempty" gorm:"comment:Timestamp when reservation refund committed"`
}

// BillingOperationLogOutbox keeps the consume-log write recoverable across log databases.
type BillingOperationLogOutbox struct {
	Id                 int64      `json:"id" gorm:"primaryKey;autoIncrement;comment:Billing log outbox numeric identifier"`
	BillingOperationId int64      `json:"billing_operation_id" gorm:"not null;uniqueIndex;comment:Billing operation owning this consume log"`
	Payload            string     `json:"-" gorm:"type:text;not null;comment:Frozen consume log payload encoded as JSON"`
	Delivered          bool       `json:"delivered" gorm:"not null;index;comment:Whether delivery to the log database completed"`
	RecordCreated      bool       `json:"record_created" gorm:"not null;comment:Whether consume logging was enabled and a log exists"`
	CreatedAt          time.Time  `json:"created_at" gorm:"not null;comment:Timestamp when the outbox record was created"`
	UpdatedAt          time.Time  `json:"updated_at" gorm:"not null;comment:Timestamp when the outbox record last changed"`
	DeliveredAt        *time.Time `json:"delivered_at,omitempty" gorm:"comment:Timestamp when log delivery completed"`
}

type BillingOperationLookupCandidate struct {
	LookupHMAC       string
	OwnerHMAC        string
	ConversationHMAC string
	KeyVersion       string
}

type BillingOperationIdentity struct {
	Candidates       []BillingOperationLookupCandidate
	ExpectedRevision uint64
	Purpose          string
	Fingerprint      string
}

type BillingOperationReserveRequest struct {
	Identity        BillingOperationIdentity
	UserId          int
	TokenId         int
	ChannelId       int
	ReservedQuota   int
	IsPlayground    bool
	PricingSnapshot string
}

type BillingOperationSettlement struct {
	ActualQuota      int
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	BillingMode      string
	CountUsage       bool
	LogUserId        int
	LogParams        RecordConsumeLogParams
}

type BillingOperationFrozenResult struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	ActualQuota      int
	BillingMode      string
	LogDelivered     bool
	LogRecorded      bool
}

type billingOperationConsumeLogPayload struct {
	UserId int                    `json:"user_id"`
	Params RecordConsumeLogParams `json:"params"`
}

func validateBillingOperationIdentity(identity BillingOperationIdentity) error {
	if len(identity.Candidates) == 0 || len(identity.Candidates) > 5 {
		return fmt.Errorf("billing operation requires between one and five lookup candidates")
	}
	if strings.TrimSpace(identity.Purpose) == "" || len(identity.Purpose) > 32 {
		return fmt.Errorf("billing operation purpose is invalid")
	}
	if strings.TrimSpace(identity.Fingerprint) == "" || len(identity.Fingerprint) > 64 {
		return fmt.Errorf("billing operation fingerprint is invalid")
	}
	seen := make(map[string]struct{}, len(identity.Candidates))
	for _, candidate := range identity.Candidates {
		if strings.TrimSpace(candidate.LookupHMAC) == "" || len(candidate.LookupHMAC) > 64 ||
			strings.TrimSpace(candidate.OwnerHMAC) == "" || len(candidate.OwnerHMAC) > 64 ||
			strings.TrimSpace(candidate.ConversationHMAC) == "" || len(candidate.ConversationHMAC) > 64 ||
			strings.TrimSpace(candidate.KeyVersion) == "" || len(candidate.KeyVersion) > 64 {
			return fmt.Errorf("billing operation lookup candidate is invalid")
		}
		key := candidate.LookupHMAC
		if _, exists := seen[key]; exists {
			return fmt.Errorf("billing operation lookup candidates contain duplicates")
		}
		seen[key] = struct{}{}
	}
	return nil
}

func findBillingOperation(tx *gorm.DB, identity BillingOperationIdentity, lock bool) (*BillingOperation, error) {
	lookupHMACs := make([]string, 0, len(identity.Candidates))
	for _, candidate := range identity.Candidates {
		lookupHMACs = append(lookupHMACs, candidate.LookupHMAC)
	}
	query := tx.Where("lookup_hmac IN ? AND expected_revision = ? AND purpose = ?", lookupHMACs, identity.ExpectedRevision, identity.Purpose)
	if lock {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var operations []BillingOperation
	if err := query.Limit(2).Find(&operations).Error; err != nil {
		return nil, err
	}
	if len(operations) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	if len(operations) > 1 {
		return nil, ErrBillingOperationLookupConflict
	}
	operation := operations[0]
	if operation.Fingerprint != identity.Fingerprint {
		return nil, ErrBillingOperationFingerprintConflict
	}
	matchedCandidate := false
	for _, candidate := range identity.Candidates {
		if operation.LookupHMAC == candidate.LookupHMAC && operation.OwnerHMAC == candidate.OwnerHMAC &&
			operation.ConversationHMAC == candidate.ConversationHMAC && operation.LookupKeyVersion == candidate.KeyVersion {
			matchedCandidate = true
			break
		}
	}
	if !matchedCandidate {
		return nil, ErrBillingOperationFingerprintConflict
	}
	return &operation, nil
}

func ReserveBillingOperation(ctx context.Context, request BillingOperationReserveRequest) (*BillingOperation, error) {
	if err := validateBillingOperationIdentity(request.Identity); err != nil {
		return nil, err
	}
	if request.UserId <= 0 || request.TokenId <= 0 || request.ChannelId <= 0 || request.ReservedQuota < 0 || strings.TrimSpace(request.PricingSnapshot) == "" {
		return nil, fmt.Errorf("billing operation reserve request is invalid")
	}
	var operation *BillingOperation
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		existing, err := findBillingOperation(tx, request.Identity, true)
		if err == nil {
			if existing.UserId != request.UserId || existing.TokenId != request.TokenId || existing.ChannelId != request.ChannelId ||
				existing.IsPlayground != request.IsPlayground {
				return ErrBillingOperationFingerprintConflict
			}
			if existing.State == BillingOperationStateRefunded {
				return ErrBillingOperationRefunded
			}
			active := request.Identity.Candidates[0]
			if existing.LookupHMAC != active.LookupHMAC {
				result := tx.Model(&BillingOperation{}).Where("id = ? AND lookup_hmac = ?", existing.Id, existing.LookupHMAC).
					Updates(map[string]interface{}{
						"lookup_hmac":        active.LookupHMAC,
						"owner_hmac":         active.OwnerHMAC,
						"conversation_hmac":  active.ConversationHMAC,
						"lookup_key_version": active.KeyVersion,
					})
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected != 1 {
					return ErrBillingOperationLookupConflict
				}
				existing.LookupHMAC = active.LookupHMAC
				existing.OwnerHMAC = active.OwnerHMAC
				existing.ConversationHMAC = active.ConversationHMAC
				existing.LookupKeyVersion = active.KeyVersion
			}
			operation = existing
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		if !request.IsPlayground && request.ReservedQuota > 0 {
			result := tx.Model(&Token{}).
				Where("id = ? AND (unlimited_quota = ? OR remain_quota >= ?)", request.TokenId, true, request.ReservedQuota).
				Updates(map[string]interface{}{
					"remain_quota":  gorm.Expr("remain_quota - ?", request.ReservedQuota),
					"used_quota":    gorm.Expr("used_quota + ?", request.ReservedQuota),
					"accessed_time": common.GetTimestamp(),
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrBillingOperationQuotaInsufficient
			}
		}

		active := request.Identity.Candidates[0]
		operation = &BillingOperation{
			LookupHMAC:       active.LookupHMAC,
			OwnerHMAC:        active.OwnerHMAC,
			ConversationHMAC: active.ConversationHMAC,
			LookupKeyVersion: active.KeyVersion,
			ExpectedRevision: request.Identity.ExpectedRevision,
			Purpose:          request.Identity.Purpose,
			Fingerprint:      request.Identity.Fingerprint,
			State:            BillingOperationStateReserved,
			UserId:           request.UserId,
			TokenId:          request.TokenId,
			ChannelId:        request.ChannelId,
			ReservedQuota:    request.ReservedQuota,
			IsPlayground:     request.IsPlayground,
			PricingSnapshot:  request.PricingSnapshot,
		}
		return tx.Create(operation).Error
	})
	if err == nil {
		return operation, nil
	}

	// A concurrent creator can win the unique business key after our initial lookup.
	// Its transaction owns the only accounting mutation, so reload and verify it.
	existing, lookupErr := findBillingOperation(DB.WithContext(ctx), request.Identity, false)
	if lookupErr == nil {
		if existing.UserId != request.UserId || existing.TokenId != request.TokenId || existing.ChannelId != request.ChannelId ||
			existing.IsPlayground != request.IsPlayground {
			return nil, ErrBillingOperationFingerprintConflict
		}
		if existing.State == BillingOperationStateRefunded {
			return nil, ErrBillingOperationRefunded
		}
		return existing, nil
	}
	return nil, err
}

func SettleBillingOperation(ctx context.Context, operationId int64, fingerprint string, settlement BillingOperationSettlement) (*BillingOperation, bool, error) {
	if operationId <= 0 || strings.TrimSpace(fingerprint) == "" {
		return nil, false, fmt.Errorf("billing operation settlement is invalid")
	}

	var operation BillingOperation
	var payload []byte
	settledNow := false
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&operation, "id = ?", operationId).Error; err != nil {
			return err
		}
		if operation.Fingerprint != fingerprint {
			return ErrBillingOperationFingerprintConflict
		}
		if operation.State == BillingOperationStateSettled {
			return nil
		}
		if operation.State == BillingOperationStateRefunded {
			return ErrBillingOperationRefunded
		}
		if settlement.ActualQuota < 0 || settlement.PromptTokens < 0 || settlement.CompletionTokens < 0 ||
			settlement.TotalTokens < 0 || settlement.TotalTokens != settlement.PromptTokens+settlement.CompletionTokens ||
			(settlement.BillingMode != "fixed" && settlement.BillingMode != "tiered_expr" && settlement.BillingMode != "free") ||
			settlement.LogUserId != operation.UserId || settlement.LogParams.TokenId != operation.TokenId ||
			settlement.LogParams.ChannelId != operation.ChannelId || settlement.LogParams.Quota != settlement.ActualQuota ||
			settlement.LogParams.PromptTokens != settlement.PromptTokens ||
			settlement.LogParams.CompletionTokens != settlement.CompletionTokens {
			return ErrBillingOperationFingerprintConflict
		}
		var err error
		payload, err = common.Marshal(billingOperationConsumeLogPayload{UserId: settlement.LogUserId, Params: settlement.LogParams})
		if err != nil {
			return fmt.Errorf("marshal billing operation log payload: %w", err)
		}

		delta := settlement.ActualQuota - operation.ReservedQuota
		if !operation.IsPlayground && delta != 0 {
			updates := map[string]interface{}{"accessed_time": common.GetTimestamp()}
			query := tx.Model(&Token{}).Where("id = ?", operation.TokenId)
			if delta > 0 {
				query = query.Where("unlimited_quota = ? OR remain_quota >= ?", true, delta)
				updates["remain_quota"] = gorm.Expr("remain_quota - ?", delta)
				updates["used_quota"] = gorm.Expr("used_quota + ?", delta)
			} else {
				refund := -delta
				query = query.Where("used_quota >= ?", refund)
				updates["remain_quota"] = gorm.Expr("remain_quota + ?", refund)
				updates["used_quota"] = gorm.Expr("used_quota - ?", refund)
			}
			result := query.Updates(updates)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrBillingOperationQuotaInsufficient
			}
		}

		if settlement.CountUsage {
			userResult := tx.Model(&User{}).Where("id = ?", operation.UserId).Updates(map[string]interface{}{
				"used_quota":    gorm.Expr("used_quota + ?", settlement.ActualQuota),
				"request_count": gorm.Expr("request_count + ?", 1),
			})
			if userResult.Error != nil {
				return userResult.Error
			}
			if userResult.RowsAffected != 1 {
				return fmt.Errorf("billing operation user does not exist")
			}
			if settlement.ActualQuota == 0 {
				var channelCount int64
				if err := tx.Model(&Channel{}).Where("id = ?", operation.ChannelId).Count(&channelCount).Error; err != nil {
					return err
				}
				if channelCount != 1 {
					return fmt.Errorf("billing operation channel does not exist")
				}
			} else {
				channelResult := tx.Model(&Channel{}).Where("id = ?", operation.ChannelId).
					Update("used_quota", gorm.Expr("used_quota + ?", settlement.ActualQuota))
				if channelResult.Error != nil {
					return channelResult.Error
				}
				if channelResult.RowsAffected != 1 {
					return fmt.Errorf("billing operation channel does not exist")
				}
			}
		}

		now := time.Now()
		result := tx.Model(&BillingOperation{}).
			Where("id = ? AND state = ? AND fingerprint = ?", operation.Id, BillingOperationStateReserved, fingerprint).
			Updates(map[string]interface{}{
				"state":             BillingOperationStateSettled,
				"actual_quota":      settlement.ActualQuota,
				"prompt_tokens":     settlement.PromptTokens,
				"completion_tokens": settlement.CompletionTokens,
				"total_tokens":      settlement.TotalTokens,
				"billing_mode":      settlement.BillingMode,
				"count_usage":       settlement.CountUsage,
				"settled_at":        now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("billing operation settlement compare-and-swap failed")
		}
		outbox := BillingOperationLogOutbox{
			BillingOperationId: operation.Id,
			Payload:            string(payload),
		}
		if err := tx.Create(&outbox).Error; err != nil {
			return err
		}
		operation.State = BillingOperationStateSettled
		operation.ActualQuota = settlement.ActualQuota
		operation.PromptTokens = settlement.PromptTokens
		operation.CompletionTokens = settlement.CompletionTokens
		operation.TotalTokens = settlement.TotalTokens
		operation.BillingMode = settlement.BillingMode
		operation.CountUsage = settlement.CountUsage
		operation.SettledAt = &now
		settledNow = true
		return nil
	})
	return &operation, settledNow, err
}

func RefundBillingOperation(ctx context.Context, operationId int64, fingerprint string) (*BillingOperation, error) {
	if operationId <= 0 || strings.TrimSpace(fingerprint) == "" {
		return nil, fmt.Errorf("billing operation refund is invalid")
	}
	var operation BillingOperation
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&operation, "id = ?", operationId).Error; err != nil {
			return err
		}
		if operation.Fingerprint != fingerprint {
			return ErrBillingOperationFingerprintConflict
		}
		if operation.State == BillingOperationStateSettled || operation.State == BillingOperationStateRefunded {
			return nil
		}
		if !operation.IsPlayground && operation.ReservedQuota > 0 {
			result := tx.Model(&Token{}).Where("id = ? AND used_quota >= ?", operation.TokenId, operation.ReservedQuota).Updates(map[string]interface{}{
				"remain_quota":  gorm.Expr("remain_quota + ?", operation.ReservedQuota),
				"used_quota":    gorm.Expr("used_quota - ?", operation.ReservedQuota),
				"accessed_time": common.GetTimestamp(),
			})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("billing operation token does not exist")
			}
		}
		now := time.Now()
		result := tx.Model(&BillingOperation{}).
			Where("id = ? AND state = ? AND fingerprint = ?", operation.Id, BillingOperationStateReserved, fingerprint).
			Updates(map[string]interface{}{"state": BillingOperationStateRefunded, "refunded_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("billing operation refund compare-and-swap failed")
		}
		operation.State = BillingOperationStateRefunded
		operation.RefundedAt = &now
		return nil
	})
	return &operation, err
}

func GetBillingOperationFrozenResult(ctx context.Context, operationId int64, fingerprint string) (*BillingOperationFrozenResult, bool, error) {
	var operation BillingOperation
	if err := DB.WithContext(ctx).First(&operation, "id = ?", operationId).Error; err != nil {
		return nil, false, err
	}
	if operation.Fingerprint != fingerprint {
		return nil, false, ErrBillingOperationFingerprintConflict
	}
	if operation.State != BillingOperationStateSettled {
		return nil, false, nil
	}
	result := &BillingOperationFrozenResult{
		PromptTokens:     operation.PromptTokens,
		CompletionTokens: operation.CompletionTokens,
		TotalTokens:      operation.TotalTokens,
		ActualQuota:      operation.ActualQuota,
		BillingMode:      operation.BillingMode,
	}
	var outbox BillingOperationLogOutbox
	if err := DB.WithContext(ctx).First(&outbox, "billing_operation_id = ?", operationId).Error; err != nil {
		return nil, false, err
	}
	result.LogDelivered = outbox.Delivered
	result.LogRecorded = outbox.RecordCreated
	return result, true, nil
}

func DeliverBillingOperationConsumeLog(c *gin.Context, operationId int64) (bool, error) {
	var outbox BillingOperationLogOutbox
	if err := DB.WithContext(c.Request.Context()).First(&outbox, "billing_operation_id = ?", operationId).Error; err != nil {
		return false, err
	}
	if outbox.Delivered {
		return outbox.RecordCreated, nil
	}
	var payload billingOperationConsumeLogPayload
	if err := common.Unmarshal([]byte(outbox.Payload), &payload); err != nil {
		return false, fmt.Errorf("decode billing operation log outbox: %w", err)
	}
	payload.Params.BillingOperationId = &operationId
	recorded, err := RecordConsumeLogResult(c, payload.UserId, payload.Params)
	if err != nil {
		return false, err
	}
	now := time.Now()
	result := DB.WithContext(c.Request.Context()).Model(&BillingOperationLogOutbox{}).
		Where("id = ? AND delivered = ?", outbox.Id, false).
		Updates(map[string]interface{}{
			"delivered":      true,
			"record_created": recorded,
			"delivered_at":   now,
		})
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected == 0 {
		if err := DB.WithContext(c.Request.Context()).First(&outbox, "id = ?", outbox.Id).Error; err != nil {
			return false, err
		}
		return outbox.RecordCreated, nil
	}
	return recorded, nil
}

func ValidateBillingOperationLogBackend() error {
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		return fmt.Errorf("billing operations require a SQL log database with unique constraints; ClickHouse is unsupported")
	}
	return nil
}
