package model

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var managedProviderFileChannelBindingStates = []string{
	ManagedProviderFileLifecycleStateIntent,
	ManagedProviderFileLifecycleStateUploadDispatched,
	ManagedProviderFileLifecycleStateUploadUnknown,
	ManagedProviderFileLifecycleStateVerificationFailed,
	ManagedProviderFileLifecycleStateActive,
	ManagedProviderFileLifecycleStateDeletionPending,
	ManagedProviderFileLifecycleStateDeletionFailed,
}

func HasDueManagedProviderFileDeletions(ctx context.Context, now time.Time) (bool, error) {
	if ctx == nil || now.IsZero() {
		return false, fmt.Errorf("managed provider file deletion readiness query is invalid")
	}
	var count int64
	err := DB.WithContext(ctx).Model(&ManagedProviderFileDeletionOutbox{}).
		Where("((state IN ? AND attempt_count < max_attempts AND next_attempt_at <= ? AND (lease_expires_at IS NULL OR lease_expires_at < ?)) OR (state = ? AND lease_expires_at < ?))",
			[]string{ManagedProviderFileDeletionOutboxStatePending, ManagedProviderFileDeletionOutboxStateRetryWait}, now, now,
			ManagedProviderFileDeletionOutboxStateInProgress, now).
		Limit(1).Count(&count).Error
	return count > 0, err
}

func GetManagedProviderFileDeletionLifecycle(ctx context.Context, lifecycleID int64) (*ManagedProviderFileLifecycle, error) {
	if ctx == nil || lifecycleID <= 0 {
		return nil, fmt.Errorf("managed provider file deletion lifecycle identity is invalid")
	}
	var lifecycle ManagedProviderFileLifecycle
	if err := DB.WithContext(ctx).First(&lifecycle, "id = ?", lifecycleID).Error; err != nil {
		return nil, err
	}
	return &lifecycle, nil
}

type ManagedProviderFileDeletionDispatch struct {
	OutboxID        int64
	LifecycleID     int64
	ExpectedVersion int64
	LeaseTokenHMAC  string
	AttemptCount    int
	DispatchedAt    time.Time
	Event           ManagedProviderFileLifecycleEvent
}

func MarkManagedProviderFileDeletionDispatched(ctx context.Context, dispatch ManagedProviderFileDeletionDispatch) (*ManagedProviderFileDeletionOutbox, error) {
	if ctx == nil || dispatch.OutboxID <= 0 || dispatch.LifecycleID <= 0 || dispatch.ExpectedVersion <= 0 || dispatch.AttemptCount <= 0 ||
		!validManagedProviderFileDigest(dispatch.LeaseTokenHMAC) || dispatch.DispatchedAt.IsZero() {
		return nil, fmt.Errorf("managed provider file deletion dispatch marker is invalid")
	}
	dispatch.DispatchedAt = dispatch.DispatchedAt.UTC()
	now := time.Now()
	var outbox ManagedProviderFileDeletionOutbox
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var lifecycle ManagedProviderFileLifecycle
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&lifecycle, "id = ?", dispatch.LifecycleID).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&outbox, "id = ? AND lifecycle_id = ?", dispatch.OutboxID, dispatch.LifecycleID).Error; err != nil {
			return err
		}
		if outbox.State != ManagedProviderFileDeletionOutboxStateInProgress || outbox.Version != dispatch.ExpectedVersion ||
			outbox.LeaseTokenHMAC != dispatch.LeaseTokenHMAC || outbox.AttemptCount != dispatch.AttemptCount || outbox.DispatchedAt != nil ||
			outbox.LeaseExpiresAt == nil || !outbox.LeaseExpiresAt.After(dispatch.DispatchedAt) || !outbox.LeaseExpiresAt.After(now) {
			return ErrManagedProviderFileDeletionLeaseLost
		}
		if dispatch.Event.LifecycleId != lifecycle.Id || dispatch.Event.EventType != ManagedProviderFileLifecycleEventDeletionDispatched ||
			dispatch.Event.AttemptCount != outbox.AttemptCount {
			return fmt.Errorf("managed provider file deletion dispatch event is invalid")
		}
		result := tx.Model(&ManagedProviderFileDeletionOutbox{}).
			Where("id = ? AND version = ? AND state = ? AND lease_token_hmac = ? AND attempt_count = ? AND dispatched_at IS NULL",
				outbox.Id, dispatch.ExpectedVersion, ManagedProviderFileDeletionOutboxStateInProgress, dispatch.LeaseTokenHMAC, dispatch.AttemptCount).
			Updates(map[string]interface{}{"version": dispatch.ExpectedVersion + 1, "dispatched_at": dispatch.DispatchedAt})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrManagedProviderFileDeletionLeaseLost
		}
		if err := appendEventAndAdvanceManagedProviderFileLifecycle(tx, &lifecycle, lifecycle.Version, nil, &dispatch.Event); err != nil {
			return err
		}
		outbox.Version++
		outbox.DispatchedAt = &dispatch.DispatchedAt
		return nil
	})
	return &outbox, err
}

func QuarantineOrphanedManagedProviderFileDeletion(ctx context.Context, outboxID, expectedVersion int64) (bool, error) {
	if ctx == nil || outboxID <= 0 || expectedVersion <= 0 {
		return false, fmt.Errorf("managed provider file deletion quarantine identity is invalid")
	}
	now := time.Now().UTC()
	result := DB.WithContext(ctx).Model(&ManagedProviderFileDeletionOutbox{}).
		Where("id = ? AND version = ? AND state IN ?", outboxID, expectedVersion, []string{
			ManagedProviderFileDeletionOutboxStatePending,
			ManagedProviderFileDeletionOutboxStateRetryWait,
			ManagedProviderFileDeletionOutboxStateInProgress,
		}).Updates(map[string]interface{}{
		"state": ManagedProviderFileDeletionOutboxStateTerminalFailed, "version": expectedVersion + 1,
		"attempt_count":    gorm.Expr("CASE WHEN attempt_count < ? THEN ? ELSE attempt_count END", 1, 1),
		"lease_token_hmac": "", "lease_expires_at": nil, "last_error_code": "lifecycle_missing",
		"terminal_result": ManagedProviderFileDeletionResultFailed, "completed_at": now,
	})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

func HasManagedProviderFileChannelBindings(ctx context.Context, channelIDs []int) (bool, error) {
	if ctx == nil {
		return false, fmt.Errorf("managed provider file channel query context is invalid")
	}
	return hasManagedProviderFileChannelBindings(DB.WithContext(ctx), channelIDs)
}

func hasManagedProviderFileChannelBindings(database *gorm.DB, channelIDs []int) (bool, error) {
	if database == nil || len(channelIDs) == 0 {
		return false, nil
	}
	for _, channelID := range channelIDs {
		if channelID <= 0 {
			return false, fmt.Errorf("managed provider file channel identity is invalid")
		}
	}
	var count int64
	err := database.Model(&ManagedProviderFileLifecycle{}).
		Where("channel_id IN ? AND state IN ?", channelIDs, managedProviderFileChannelBindingStates).
		Limit(1).Count(&count).Error
	return count > 0, err
}

func EnsureManagedProviderFileChannelsMutable(ctx context.Context, channelIDs []int) error {
	if ctx == nil {
		return fmt.Errorf("managed provider file channel mutation context is invalid")
	}
	return ensureManagedProviderFileChannelsMutable(DB.WithContext(ctx), channelIDs)
}

func ensureManagedProviderFileChannelsMutable(database *gorm.DB, channelIDs []int) error {
	bound, err := hasManagedProviderFileChannelBindings(database, channelIDs)
	if err != nil {
		return err
	}
	if bound {
		return ErrManagedProviderFileChannelBound
	}
	return nil
}

func ManagedProviderFileProtectedChannelFieldsChanged(existing, requested *Channel) bool {
	if existing == nil || requested == nil || existing.Id <= 0 || requested.Id != existing.Id {
		return true
	}
	if requested.Type != 0 && requested.Type != existing.Type {
		return true
	}
	if requested.Key != "" && requested.Key != existing.Key {
		return true
	}
	if requested.BaseURL != nil && !sameManagedProviderFileStringPointer(requested.BaseURL, existing.BaseURL) {
		return true
	}
	if requested.OpenAIOrganization != nil && !sameManagedProviderFileStringPointer(requested.OpenAIOrganization, existing.OpenAIOrganization) {
		return true
	}
	if requested.Setting != nil && !sameManagedProviderFileStringPointer(requested.Setting, existing.Setting) {
		return true
	}
	if requested.ParamOverride != nil && !sameManagedProviderFileStringPointer(requested.ParamOverride, existing.ParamOverride) {
		return true
	}
	if requested.HeaderOverride != nil && !sameManagedProviderFileStringPointer(requested.HeaderOverride, existing.HeaderOverride) {
		return true
	}
	return (requested.OtherSettings != "" && requested.OtherSettings != existing.OtherSettings) ||
		requested.ChannelInfo.IsMultiKey != existing.ChannelInfo.IsMultiKey
}

func sameManagedProviderFileStringPointer(left, right *string) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}
