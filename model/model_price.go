package model

import (
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

type modelBasePriceEntry struct {
	ModelName string
	NameRule  int
	Price     float64
}

var (
	modelBasePriceLock     sync.RWMutex
	modelExactBasePriceMap = make(map[string]float64)
	modelPrefixBasePrices  []modelBasePriceEntry
	modelSuffixBasePrices  []modelBasePriceEntry
	modelContainsBasePrice []modelBasePriceEntry
	modelBasePriceLoadedAt time.Time
)

func refreshModelBasePriceCache(models []Model) {
	exactPrices := make(map[string]float64)
	prefixPrices := make([]modelBasePriceEntry, 0)
	suffixPrices := make([]modelBasePriceEntry, 0)
	containsPrices := make([]modelBasePriceEntry, 0)
	for i := range models {
		current := &models[i]
		if current.BasePrice == nil || current.Status != 1 {
			continue
		}
		entry := modelBasePriceEntry{
			ModelName: current.ModelName,
			NameRule:  current.NameRule,
			Price:     *current.BasePrice,
		}
		switch current.NameRule {
		case NameRuleExact:
			exactPrices[current.ModelName] = *current.BasePrice
		case NameRulePrefix:
			prefixPrices = append(prefixPrices, entry)
		case NameRuleSuffix:
			suffixPrices = append(suffixPrices, entry)
		case NameRuleContains:
			containsPrices = append(containsPrices, entry)
		}
	}

	modelBasePriceLock.Lock()
	modelExactBasePriceMap = exactPrices
	modelPrefixBasePrices = prefixPrices
	modelSuffixBasePrices = suffixPrices
	modelContainsBasePrice = containsPrices
	modelBasePriceLoadedAt = time.Now()
	modelBasePriceLock.Unlock()
}

func invalidateModelBasePriceCache() {
	modelBasePriceLock.Lock()
	modelExactBasePriceMap = make(map[string]float64)
	modelPrefixBasePrices = nil
	modelSuffixBasePrices = nil
	modelContainsBasePrice = nil
	modelBasePriceLoadedAt = time.Time{}
	modelBasePriceLock.Unlock()
}

func ensureModelBasePriceCache() {
	modelBasePriceLock.RLock()
	loaded := !modelBasePriceLoadedAt.IsZero()
	modelBasePriceLock.RUnlock()
	if loaded || DB == nil {
		return
	}

	modelBasePriceLock.Lock()
	loaded = !modelBasePriceLoadedAt.IsZero()
	modelBasePriceLock.Unlock()
	if loaded {
		return
	}

	var models []Model
	if err := DB.Select("model_name", "base_price", "name_rule", "status").Where("base_price IS NOT NULL AND status = ?", 1).Find(&models).Error; err != nil {
		common.SysError("load model base price cache failed: " + err.Error())
		return
	}
	refreshModelBasePriceCache(models)
}

func getModelConfiguredBasePriceFromCache(name string) (float64, bool) {
	ensureModelBasePriceCache()

	modelBasePriceLock.RLock()
	defer modelBasePriceLock.RUnlock()

	if price, ok := modelExactBasePriceMap[name]; ok {
		return price, true
	}
	for _, entry := range modelPrefixBasePrices {
		if strings.HasPrefix(name, entry.ModelName) {
			return entry.Price, true
		}
	}
	for _, entry := range modelSuffixBasePrices {
		if strings.HasSuffix(name, entry.ModelName) {
			return entry.Price, true
		}
	}
	for _, entry := range modelContainsBasePrice {
		if strings.Contains(name, entry.ModelName) {
			return entry.Price, true
		}
	}
	return 0, false
}

func GetModelFixedPrice(name string, printErr bool) (float64, bool) {
	formattedName := ratio_setting.FormatMatchingModelName(name)
	if price, ok := getModelConfiguredBasePriceFromCache(formattedName); ok {
		return price, true
	}

	price, ok := ratio_setting.GetModelPrice(name, false)
	if ok {
		return price, true
	}

	if printErr {
		common.SysError("model price not found: " + formattedName)
	}
	return -1, false
}

func HasModelConfiguredBasePrice(name string) bool {
	_, ok := getModelConfiguredBasePriceFromCache(ratio_setting.FormatMatchingModelName(name))
	return ok
}
