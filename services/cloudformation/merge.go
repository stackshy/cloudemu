package cloudformation

import "encoding/json"

// sectionResources is the template section that declares resources.
const sectionResources = "Resources"

// MergeResources returns newBody as JSON with each resource named in useOld
// set back to its definition in oldBody, or dropped when oldBody has none.
// It records the last applied state of a stack whose update failed without
// a rollback: resources that were updated keep the new definition, and the
// rest keep the old one.
func MergeResources(newBody, oldBody string, useOld map[string]bool) (string, error) {
	newTree, err := decodeTemplate(newBody)
	if err != nil {
		return "", err
	}

	oldTree, err := decodeTemplate(oldBody)
	if err != nil {
		return "", err
	}

	newTop, _ := newTree.(map[string]any)
	oldTop, _ := oldTree.(map[string]any)
	newRes, _ := newTop[sectionResources].(map[string]any)
	oldRes, _ := oldTop[sectionResources].(map[string]any)

	if newRes == nil {
		newRes = map[string]any{}
		newTop[sectionResources] = newRes
	}

	for id := range useOld {
		if def, ok := oldRes[id]; ok {
			newRes[id] = def
		} else {
			delete(newRes, id)
		}
	}

	out, err := json.Marshal(newTop)
	if err != nil {
		return "", err
	}

	return string(out), nil
}
