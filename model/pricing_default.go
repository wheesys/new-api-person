package model

func initDefaultModelMetadata(metaMap map[string]*Model, enableAbilities []AbilityWithChannel) {
	for _, ability := range enableAbilities {
		modelName := ability.Model
		if _, exists := metaMap[modelName]; exists {
			continue
		}

		metaMap[modelName] = &Model{
			ModelName: modelName,
			Status:    1,
			NameRule:  NameRuleExact,
		}
	}
}
