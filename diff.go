package audit

import "reflect"

// diffResult returns the old/new maps to persist for an update.
// Only fields whose values changed are kept; exclude fields are dropped from
// both sides. For creates old is nil; for deletes new is nil.
func diffResult(action string, old, new map[string]any, exclude []string) (oldOut, newOut map[string]any) {
	exSet := toSet(exclude)
	switch action {
	case ActionCreate:
		return nil, dropExcluded(new, exSet)
	case ActionDelete:
		return dropExcluded(old, exSet), nil
	case ActionSoftDelete, ActionRestore:
		return dropExcluded(old, exSet), dropExcluded(new, exSet)
	case ActionUpdate:
		oldOut = map[string]any{}
		newOut = map[string]any{}
		// Compare keys from both sides.
		keys := map[string]struct{}{}
		for k := range old {
			keys[k] = struct{}{}
		}
		for k := range new {
			keys[k] = struct{}{}
		}
		for k := range keys {
			if _, skip := exSet[k]; skip {
				continue
			}
			ov, oOk := old[k]
			nv, nOk := new[k]
			if !oOk && !nOk {
				continue
			}
			if reflect.DeepEqual(ov, nv) {
				continue
			}
			if oOk {
				oldOut[k] = ov
			}
			if nOk {
				newOut[k] = nv
			}
		}
		if len(oldOut) == 0 {
			oldOut = nil
		}
		if len(newOut) == 0 {
			newOut = nil
		}
		return oldOut, newOut
	}
	return old, new
}

func dropExcluded(in map[string]any, ex map[string]struct{}) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		if _, skip := ex[k]; skip {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func toSet(s []string) map[string]struct{} {
	m := make(map[string]struct{}, len(s))
	for _, v := range s {
		m[v] = struct{}{}
	}
	return m
}
