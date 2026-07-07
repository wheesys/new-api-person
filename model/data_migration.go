package model

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	dataMigrationVersionOptionKey = "DataMigrationVersion"
	currentDataMigrationVersion   = 2026070701
)

type dataMigration struct {
	version int
	name    string
	run     func(tx *gorm.DB) error
}

var dataMigrations = []dataMigration{
	{
		version: 2026070701,
		name:    "legacy channel usage metadata backfill",
		run:     migrateLegacyChannelUsageMetadata,
	},
}

func RunDataMigrations() error {
	if DB == nil {
		return errors.New("database is not initialized")
	}

	version, err := getDataMigrationVersion(DB)
	if err != nil {
		return err
	}

	migrated := false
	for _, migration := range dataMigrations {
		if migration.version <= version {
			continue
		}
		if err := DB.Transaction(func(tx *gorm.DB) error {
			if err := migration.run(tx); err != nil {
				return err
			}
			return setDataMigrationVersion(tx, migration.version)
		}); err != nil {
			return fmt.Errorf("data migration %d %s failed: %w", migration.version, migration.name, err)
		}
		common.SysLog(fmt.Sprintf("data migration completed: version=%d name=%s", migration.version, migration.name))
		migrated = true
	}

	if migrated {
		InvalidatePricingCache()
	}
	return nil
}

func getDataMigrationVersion(tx *gorm.DB) (int, error) {
	var option Option
	err := tx.First(&option, "key = ?", dataMigrationVersionOptionKey).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	value := strings.TrimSpace(option.Value)
	if value == "" {
		return 0, nil
	}
	version, err := strconv.Atoi(value)
	if err != nil {
		common.SysLog(fmt.Sprintf("invalid data migration version %q, rerunning migrations from version 0", value))
		return 0, nil
	}
	return version, nil
}

func setDataMigrationVersion(tx *gorm.DB, version int) error {
	return tx.Save(&Option{
		Key:   dataMigrationVersionOptionKey,
		Value: strconv.Itoa(version),
	}).Error
}

func migrateLegacyChannelUsageMetadata(tx *gorm.DB) error {
	priceRatioResult := tx.Model(&Channel{}).
		Where("price_ratio IS NULL").
		Update("price_ratio", 1)
	if priceRatioResult.Error != nil {
		return priceRatioResult.Error
	}

	var abilityCount int64
	if err := tx.Model(&Ability{}).Count(&abilityCount).Error; err != nil {
		return err
	}
	if abilityCount == 0 {
		if err := backfillAbilitiesFromLegacyChannels(tx); err != nil {
			return err
		}
	}

	modelNames, err := collectLegacyChannelModelNames(tx)
	if err != nil {
		return err
	}
	createdModels, err := ensureModelMetadata(tx, modelNames)
	if err != nil {
		return err
	}

	common.SysLog(fmt.Sprintf("legacy channel migration backfilled price_ratio rows=%d models=%d",
		priceRatioResult.RowsAffected, createdModels))
	return nil
}

func backfillAbilitiesFromLegacyChannels(tx *gorm.DB) error {
	var channels []Channel
	if err := tx.Where("models <> ''").Find(&channels).Error; err != nil {
		return err
	}
	for i := range channels {
		if err := channels[i].AddAbilities(tx); err != nil {
			return err
		}
	}
	return nil
}

func collectLegacyChannelModelNames(tx *gorm.DB) ([]string, error) {
	modelNames := make([]string, 0)

	var abilityModelNames []string
	if err := tx.Model(&Ability{}).Distinct("model").Pluck("model", &abilityModelNames).Error; err != nil {
		return nil, err
	}
	modelNames = append(modelNames, abilityModelNames...)

	var channels []Channel
	if err := tx.Model(&Channel{}).Select("models").Where("models <> ''").Find(&channels).Error; err != nil {
		return nil, err
	}
	for _, channel := range channels {
		modelNames = append(modelNames, channel.GetModels()...)
	}

	return normalizeModelMetadataNames(modelNames), nil
}
