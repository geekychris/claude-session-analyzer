package parse

import "sort"

// MergeByTime merges record lists (e.g. a session file and its sub-agent
// files) into one, ordered by timestamp. The sort is stable; a record without
// a timestamp sorts with the previous timestamped record of its own list.
func MergeByTime(lists ...[]Record) []Record {
	type keyed struct {
		rec  Record
		key  string
		list int
		pos  int
	}
	var all []keyed
	for li, l := range lists {
		last := ""
		for pi, r := range l {
			if r.Timestamp != "" {
				last = r.Timestamp
			}
			all = append(all, keyed{rec: r, key: last, list: li, pos: pi})
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].list == all[j].list {
			return all[i].pos < all[j].pos
		}
		ti, tj := all[i].key, all[j].key
		if ti != tj {
			return ti < tj
		}
		return all[i].list < all[j].list
	})
	out := make([]Record, len(all))
	for i, k := range all {
		out[i] = k.rec
	}
	return out
}
