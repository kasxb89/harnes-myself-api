package controller

import (
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/ycapi"
)

// New YC contracts require the V2 execution envelope. Keep them out of legacy
// selectable catalogs without hiding their configuration or financial records.
func mediaV2OnlyCatalogModel(name string, record model.PublishedModelConfiguration) bool {
	if record.Definition.MediaExecution == nil {
		return false
	}
	if _, legacy := ycapi.Lookup(name); legacy {
		return false
	}
	contract, declared := ycapi.LookupMediaContract(name)
	return declared && contract.ID == name
}

func legacySelectableModelNames(names []string) ([]string, error) {
	records, err := model.CurrentPublishedModelConfigurations()
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(names))
	for _, name := range names {
		if record, published := records[name]; published && mediaV2OnlyCatalogModel(name, record) {
			continue
		}
		result = append(result, name)
	}
	return result, nil
}
