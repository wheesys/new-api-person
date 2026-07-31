package model

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/samber/lo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func BatchDeleteChannels(ids []int) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	tx := DB.Begin()
	if tx.Error != nil {
		return 0, tx.Error
	}
	var lockedChannels []Channel
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ?", ids).Order("id asc").Find(&lockedChannels).Error; err != nil {
		tx.Rollback()
		return 0, err
	}
	if err := ensureManagedProviderFileChannelsMutable(tx, ids); err != nil {
		tx.Rollback()
		return 0, err
	}
	var deletedCount int64
	for _, chunk := range lo.Chunk(ids, 200) {
		result := tx.Where("id in (?)", chunk).Delete(&Channel{})
		if result.Error != nil {
			tx.Rollback()
			return 0, result.Error
		}
		deletedCount += result.RowsAffected
		if err := tx.Where("channel_id in (?)", chunk).Delete(&Ability{}).Error; err != nil {
			tx.Rollback()
			return 0, err
		}
	}
	if err := tx.Commit().Error; err != nil {
		return 0, err
	}
	return deletedCount, nil
}

func (channel *Channel) Update() error {
	existingChannel, existingErr := GetChannelById(channel.Id, true)
	if existingErr != nil {
		return existingErr
	}
	if channel.ChannelInfo.IsMultiKey {
		keyString := channel.Key
		if keyString == "" {
			keyString = existingChannel.Key
		}
		keys := []string{}
		if keyString != "" {
			trimmedKey := strings.TrimSpace(keyString)
			if strings.HasPrefix(trimmedKey, "[") {
				var rawKeys []json.RawMessage
				if err := common.Unmarshal([]byte(trimmedKey), &rawKeys); err == nil {
					keys = make([]string, len(rawKeys))
					for index, rawKey := range rawKeys {
						keys[index] = string(rawKey)
					}
				}
			}
			if len(keys) == 0 {
				keys = strings.Split(strings.Trim(keyString, "\n"), "\n")
			}
		}
		channel.ChannelInfo.MultiKeySize = len(keys)
		for index := range channel.ChannelInfo.MultiKeyStatusList {
			if index >= channel.ChannelInfo.MultiKeySize {
				delete(channel.ChannelInfo.MultiKeyStatusList, index)
			}
		}
	}
	tx := DB.Begin()
	if tx.Error != nil {
		return tx.Error
	}
	var lockedChannel Channel
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockedChannel, "id = ?", channel.Id).Error; err != nil {
		tx.Rollback()
		return err
	}
	if ManagedProviderFileProtectedChannelFieldsChanged(&lockedChannel, channel) {
		if err := ensureManagedProviderFileChannelsMutable(tx, []int{channel.Id}); err != nil {
			tx.Rollback()
			return err
		}
	}
	if err := tx.Model(channel).Updates(channel).Error; err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Model(channel).First(channel, "id = ?", channel.Id).Error; err != nil {
		tx.Rollback()
		return err
	}
	if err := channel.UpdateAbilities(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit().Error
}

func (channel *Channel) Delete() error {
	tx := DB.Begin()
	if tx.Error != nil {
		return tx.Error
	}
	var lockedChannel Channel
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockedChannel, "id = ?", channel.Id).Error; err != nil {
		tx.Rollback()
		return err
	}
	if err := ensureManagedProviderFileChannelsMutable(tx, []int{channel.Id}); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Delete(channel).Error; err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Where("channel_id = ?", channel.Id).Delete(&Ability{}).Error; err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit().Error
}

func EditChannelByTag(tag string, newTag *string, modelMapping *string, models *string, group *string, priority *int64, weight *uint, paramOverride *string, headerOverride *string) error {
	database := DB
	var mutationTransaction *gorm.DB
	if paramOverride != nil || headerOverride != nil {
		mutationTransaction = DB.Begin()
		if mutationTransaction.Error != nil {
			return mutationTransaction.Error
		}
		var channelIDs []int
		if err := mutationTransaction.Clauses(clause.Locking{Strength: "UPDATE"}).Model(&Channel{}).
			Where("tag = ?", tag).Order("id asc").Pluck("id", &channelIDs).Error; err != nil {
			mutationTransaction.Rollback()
			return err
		}
		if err := ensureManagedProviderFileChannelsMutable(mutationTransaction, channelIDs); err != nil {
			mutationTransaction.Rollback()
			return err
		}
		database = mutationTransaction
	}
	updateData := Channel{}
	shouldRecreateAbilities := false
	updatedTag := tag
	if newTag != nil && *newTag != tag {
		updateData.Tag = newTag
		updatedTag = *newTag
	}
	if modelMapping != nil {
		updateData.ModelMapping = modelMapping
	}
	if models != nil && *models != "" {
		shouldRecreateAbilities = true
		updateData.Models = *models
	}
	if group != nil && *group != "" {
		shouldRecreateAbilities = true
		updateData.Group = *group
	}
	if priority != nil {
		updateData.Priority = priority
	}
	if weight != nil {
		updateData.Weight = weight
	}
	if paramOverride != nil {
		updateData.ParamOverride = paramOverride
	}
	if headerOverride != nil {
		updateData.HeaderOverride = headerOverride
	}
	if err := database.Model(&Channel{}).Where("tag = ?", tag).Updates(updateData).Error; err != nil {
		if mutationTransaction != nil {
			mutationTransaction.Rollback()
		}
		return err
	}
	if mutationTransaction != nil {
		if err := mutationTransaction.Commit().Error; err != nil {
			return err
		}
	}
	if shouldRecreateAbilities {
		channels, err := GetChannelsByTag(updatedTag, false, false)
		if err == nil {
			for _, channel := range channels {
				if err := channel.UpdateAbilities(nil); err != nil {
					common.SysLog(fmt.Sprintf("failed to update abilities: channel_id=%d, tag=%s, error=%v", channel.Id, channel.GetTag(), err))
				}
			}
		}
		return nil
	}
	return UpdateAbilityByTag(tag, newTag, priority, weight)
}

func DeleteChannelByStatus(status int64) (int64, error) {
	tx := DB.Begin()
	if tx.Error != nil {
		return 0, tx.Error
	}
	var channelIDs []int
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Model(&Channel{}).Where("status = ?", status).Order("id asc").Pluck("id", &channelIDs).Error; err != nil {
		tx.Rollback()
		return 0, err
	}
	if err := ensureManagedProviderFileChannelsMutable(tx, channelIDs); err != nil {
		tx.Rollback()
		return 0, err
	}
	result := tx.Where("status = ?", status).Delete(&Channel{})
	if result.Error != nil {
		tx.Rollback()
		return 0, result.Error
	}
	return result.RowsAffected, tx.Commit().Error
}

func DeleteDisabledChannel() (int64, error) {
	tx := DB.Begin()
	if tx.Error != nil {
		return 0, tx.Error
	}
	var channelIDs []int
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Model(&Channel{}).
		Where("status = ? or status = ?", common.ChannelStatusAutoDisabled, common.ChannelStatusManuallyDisabled).
		Order("id asc").Pluck("id", &channelIDs).Error; err != nil {
		tx.Rollback()
		return 0, err
	}
	if err := ensureManagedProviderFileChannelsMutable(tx, channelIDs); err != nil {
		tx.Rollback()
		return 0, err
	}
	result := tx.Where("status = ? or status = ?", common.ChannelStatusAutoDisabled, common.ChannelStatusManuallyDisabled).Delete(&Channel{})
	if result.Error != nil {
		tx.Rollback()
		return 0, result.Error
	}
	return result.RowsAffected, tx.Commit().Error
}
