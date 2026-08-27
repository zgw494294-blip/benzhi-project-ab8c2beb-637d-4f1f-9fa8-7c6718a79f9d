package archive

import "strings"

func consentChanges(before *ConsentScope, after ConsentScope) []FieldChange {
	changes := []FieldChange{}
	add := func(field, oldValue, newValue string) {
		if oldValue != newValue {
			changes = append(changes, FieldChange{Field: field, Before: oldValue, After: newValue})
		}
	}
	if before == nil {
		add("allowedUses", "", strings.Join(after.AllowedUses, "、"))
		add("restrictedTopics", "", strings.Join(after.RestrictedTopics, "、"))
		add("nameDisclosure", "", string(after.NameDisclosure))
		add("sealedUntil", "", after.SealedUntil)
		return changes
	}
	add("allowedUses", strings.Join(before.AllowedUses, "、"), strings.Join(after.AllowedUses, "、"))
	add("restrictedTopics", strings.Join(before.RestrictedTopics, "、"), strings.Join(after.RestrictedTopics, "、"))
	add("nameDisclosure", string(before.NameDisclosure), string(after.NameDisclosure))
	add("sealedUntil", before.SealedUntil, after.SealedUntil)
	return changes
}
