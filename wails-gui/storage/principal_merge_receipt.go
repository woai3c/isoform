package storage

import "encoding/json"

const peopleMergeReceiptVersion = 2

// A receipt stores its own source rows and the target's original baseline.
// It never copies a sibling merge's aliases, handles, messages or counters.
func isolatePeopleMergeReceipt(target string, before, after, baseline []mergeTableSnapshot, aliasBase map[string]bool) peopleMergeReceipt {
	receipt := peopleMergeReceipt{Version: peopleMergeReceiptVersion, Before: make([]mergeTableSnapshot, len(before)), After: make([]mergeTableSnapshot, len(after)), Baseline: baseline, AliasBase: aliasBase}
	for i := range before {
		receipt.Before[i] = mergeTableSnapshot{Columns: before[i].Columns, Rows: []mergeStoredRow{}}
		receipt.After[i] = mergeTableSnapshot{Columns: after[i].Columns, Rows: []mergeStoredRow{}}
		for _, row := range before[i].Rows {
			if i == 0 || mergeText(before[i], row, "principal_id") != target {
				receipt.Before[i].Rows = append(receipt.Before[i].Rows, row)
			}
		}
		for _, row := range after[i].Rows {
			if i == 0 || mergeText(after[i], row, "principal_id") != target {
				receipt.After[i].Rows = append(receipt.After[i].Rows, row)
			}
		}
	}
	// Only previews introduced by this merge are candidates for Undo removal.
	existing := mergeRowsByID(before[4].Rows)
	for _, row := range mergeOwned(after[4], target) {
		if _, ok := existing[row.ID]; !ok {
			receipt.After[4].Rows = append(receipt.After[4].Rows, row)
		}
	}
	for _, index := range []int{3, 5, 7} {
		type contribution struct {
			count, first, last int64
			present            bool
		}
		totals := map[string]contribution{}
		add := func(key string, count, first, last int64) {
			total := totals[key]
			total.count += count
			if !total.present || first < total.first {
				total.first = first
			}
			if !total.present || last > total.last {
				total.last = last
			}
			total.present = true
			totals[key] = total
		}
		for _, sourceTable := range []int{3, 5} {
			if index != 7 && index != sourceTable {
				continue
			}
			for _, row := range before[sourceTable].Rows {
				snapshot := before[sourceTable]
				if mergeText(snapshot, row, "principal_id") == target {
					continue
				}
				key := mergeAggregateKey(sourceTable, snapshot, row)
				if index == 7 {
					if sourceTable == 3 {
						key += "\x00"
					} else {
						key = "\x00" + key
					}
				}
				add(key, mergeCell(snapshot, row, "message_count").Int, mergeCell(snapshot, row, "first_seen_at").Int, mergeCell(snapshot, row, "last_seen_at").Int)
			}
		}
		for _, row := range mergeOwned(after[index], target) {
			total, ok := totals[mergeAggregateKey(index, after[index], row)]
			if !ok {
				continue
			}
			row.Values = append([]mergeValue(nil), row.Values...)
			mergeSet(after[index], &row, "message_count", mergeValue{Kind: "int", Int: total.count})
			mergeSet(after[index], &row, "first_seen_at", mergeValue{Kind: "int", Int: total.first})
			mergeSet(after[index], &row, "last_seen_at", mergeValue{Kind: "int", Int: total.last})
			receipt.After[index].Rows = append(receipt.After[index].Rows, row)
		}
	}
	return receipt
}

func decodePeopleMergeReceipt(raw, target string) (peopleMergeReceipt, error) {
	var receipt peopleMergeReceipt
	if json.Unmarshal([]byte(raw), &receipt) != nil || !validPeopleMergeReceipt(receipt, target) {
		return peopleMergeReceipt{}, &MergeStateError{Code: "invalid_receipt"}
	}
	return receipt, nil
}
