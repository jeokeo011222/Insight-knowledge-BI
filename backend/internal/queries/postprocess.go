package queries

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// This file implements the pandas post-processing operators superset applies
// after SQL execution (superset/utils/pandas_postprocessing/*) on top of the
// generic row maps returned by the driver.

// ApplyPostProcessing runs the operators sequentially. Unknown operators return
// an error instead of silently passing the data through.
func ApplyPostProcessing(data []map[string]any, ops []map[string]any) ([]map[string]any, error) {
	out := data
	if out == nil {
		out = []map[string]any{}
	}
	for _, op := range ops {
		name := strings.ToLower(firstString(op["operation"]))
		if name == "" {
			return nil, fmt.Errorf("post-processing operation is required")
		}
		options := asStringMap(op["options"])
		next, err := applyPostProcessingOperation(name, out, options)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		out = next
	}
	return out, nil
}

func applyPostProcessingOperation(name string, data []map[string]any, opts map[string]any) ([]map[string]any, error) {
	switch name {
	case "pivot":
		return ppPivot(data, opts)
	case "aggregate":
		return ppAggregate(data, opts)
	case "rolling":
		return ppRolling(data, opts)
	case "cum":
		return ppCum(data, opts)
	case "contribution":
		return ppContribution(data, opts)
	case "rank":
		return ppRank(data, opts)
	case "sort":
		return ppSort(data, opts)
	case "rename":
		return ppRename(data, opts)
	case "flatten":
		return ppFlatten(data, opts)
	case "compare":
		return ppCompare(data, opts)
	case "resample":
		return ppResample(data, opts)
	case "time_compare":
		return ppTimeCompare(data, opts)
	case "geohash_decode":
		return ppGeohashDecode(data, opts)
	}
	return nil, fmt.Errorf("unsupported post-processing operation %q", name)
}

// ppSort orders rows by the requested columns.
func ppSort(data []map[string]any, opts map[string]any) ([]map[string]any, error) {
	by := toStringList(opts["by"])
	if len(by) == 0 {
		return data, nil
	}
	ascending := toBoolList(opts["ascending"], len(by))
	out := make([]map[string]any, len(data))
	copy(out, data)
	sort.SliceStable(out, func(i, j int) bool {
		for idx, col := range by {
			a, b := out[i][col], out[j][col]
			cmp := compareValues(a, b)
			if cmp == 0 {
				continue
			}
			if idx < len(ascending) && !ascending[idx] {
				return cmp > 0
			}
			return cmp < 0
		}
		return false
	})
	return out, nil
}

// ppRename renames columns.
func ppRename(data []map[string]any, opts map[string]any) ([]map[string]any, error) {
	mapping := asStringMap(opts["columns"])
	if len(mapping) == 0 {
		return data, nil
	}
	for _, row := range data {
		for from, to := range mapping {
			name := firstString(to)
			if name == "" {
				continue
			}
			if v, ok := row[from]; ok {
				delete(row, from)
				row[name] = v
			}
		}
	}
	return data, nil
}

// ppAggregate groups rows and applies aggregate functions.
func ppAggregate(data []map[string]any, opts map[string]any) ([]map[string]any, error) {
	groupby := toStringList(opts["groupby"])
	aggs := aggregateSpec(opts)
	if len(aggs) == 0 {
		return nil, fmt.Errorf("aggregate requires an aggregates mapping")
	}

	type bucket struct {
		key  string
		vals map[string][]float64
		base map[string]any
	}
	buckets := map[string]*bucket{}
	order := []string{}
	for _, row := range data {
		key := groupKey(row, groupby)
		b, ok := buckets[key]
		if !ok {
			b = &bucket{key: key, vals: map[string][]float64{}, base: map[string]any{}}
			for _, col := range groupby {
				b.base[col] = row[col]
			}
			buckets[key] = b
			order = append(order, key)
		}
		for col := range aggs {
			if n, ok := numOf(row[col]); ok {
				b.vals[col] = append(b.vals[col], n)
			}
		}
	}

	out := make([]map[string]any, 0, len(order))
	for _, key := range order {
		b := buckets[key]
		row := map[string]any{}
		for k, v := range b.base {
			row[k] = v
		}
		for col, fn := range aggs {
			v, err := aggregateValues(b.vals[col], fn)
			if err != nil {
				return nil, err
			}
			row[col] = v
		}
		out = append(out, row)
	}
	return out, nil
}

// ppPivot converts long rows into a wide, pivoted result.
func ppPivot(data []map[string]any, opts map[string]any) ([]map[string]any, error) {
	index := toStringList(opts["index"])
	columns := toStringList(opts["columns"])
	aggs := aggregateSpec(opts)
	if len(index) == 0 {
		return nil, fmt.Errorf("pivot requires index columns")
	}
	if len(aggs) == 0 {
		return nil, fmt.Errorf("pivot requires an aggregates mapping")
	}

	type bucket struct {
		base map[string]any
		vals map[string][]float64
	}
	buckets := map[string]*bucket{}
	order := []string{}
	labels := map[string]bool{}
	labelOrder := []string{}

	for _, row := range data {
		key := groupKey(row, index)
		b, ok := buckets[key]
		if !ok {
			b = &bucket{base: map[string]any{}, vals: map[string][]float64{}}
			for _, col := range index {
				b.base[col] = row[col]
			}
			buckets[key] = b
			order = append(order, key)
		}
		label := groupKey(row, columns)
		if !labels[label] {
			labels[label] = true
			labelOrder = append(labelOrder, label)
		}
		for col := range aggs {
			cell := col
			if label != "" {
				cell = col + "__" + label
			}
			if n, ok := numOf(row[col]); ok {
				b.vals[cell] = append(b.vals[cell], n)
			}
		}
	}

	fill, hasFill := opts["metric_fill_value"]
	out := make([]map[string]any, 0, len(order))
	for _, key := range order {
		b := buckets[key]
		row := map[string]any{}
		for k, v := range b.base {
			row[k] = v
		}
		for col, fn := range aggs {
			for _, label := range labelOrder {
				cell := col
				name := label
				if label != "" {
					cell = col + "__" + label
				}
				if name == "" {
					name = col
				}
				v, err := aggregateValues(b.vals[cell], fn)
				if err != nil {
					return nil, err
				}
				if v == nil && hasFill {
					v = fill
				}
				row[name] = v
			}
		}
		out = append(out, row)
	}
	return out, nil
}

// ppRolling applies a trailing window function.
func ppRolling(data []map[string]any, opts map[string]any) ([]map[string]any, error) {
	columns := asStringMap(opts["columns"])
	if len(columns) == 0 {
		return nil, fmt.Errorf("rolling requires a columns mapping")
	}
	window := asInt(opts["window"])
	if window <= 0 {
		return nil, fmt.Errorf("rolling requires a window greater than zero")
	}
	rollingType := strings.ToLower(firstString(opts["rolling_type"]))
	if rollingType == "" {
		rollingType = "mean"
	}
	center := asBool(opts["center"])
	minPeriods := asInt(opts["min_periods"])

	for src, dstRaw := range columns {
		dst := firstString(dstRaw)
		if dst == "" {
			dst = src
		}
		values := make([]float64, len(data))
		present := make([]bool, len(data))
		for i, row := range data {
			if n, ok := numOf(row[src]); ok {
				values[i] = n
				present[i] = true
			}
		}
		for i := range data {
			start, end := i-window+1, i+1
			if center {
				start = i - window/2
				end = start + window
			}
			windowVals := make([]float64, 0, window)
			for j := start; j < end; j++ {
				if j >= 0 && j < len(values) && present[j] {
					windowVals = append(windowVals, values[j])
				}
			}
			if len(windowVals) == 0 || (minPeriods > 0 && len(windowVals) < minPeriods) {
				data[i][dst] = nil
				continue
			}
			v, err := aggregateValues(windowVals, rollingType)
			if err != nil {
				return nil, err
			}
			data[i][dst] = v
		}
	}
	return data, nil
}

// ppCum accumulates values across rows.
func ppCum(data []map[string]any, opts map[string]any) ([]map[string]any, error) {
	columns := asStringMap(opts["columns"])
	if len(columns) == 0 {
		return nil, fmt.Errorf("cum requires a columns mapping")
	}
	operator := strings.ToLower(firstString(opts["operator"]))
	if operator == "" {
		operator = "sum"
	}
	if operator != "sum" && operator != "prod" && operator != "min" && operator != "max" {
		return nil, fmt.Errorf("invalid cumulative operator %q", operator)
	}
	for src, dstRaw := range columns {
		dst := firstString(dstRaw)
		if dst == "" {
			dst = src
		}
		var acc float64
		count := 0
		for _, row := range data {
			n, ok := numOf(row[src])
			if !ok {
				row[dst] = nil
				continue
			}
			if count == 0 {
				acc = n
			} else {
				switch operator {
				case "sum":
					acc += n
				case "prod":
					acc *= n
				case "min":
					acc = math.Min(acc, n)
				case "max":
					acc = math.Max(acc, n)
				}
			}
			count++
			row[dst] = acc
		}
	}
	return data, nil
}

// ppContribution expresses each cell as a share of the row/column total.
func ppContribution(data []map[string]any, opts map[string]any) ([]map[string]any, error) {
	orientation := strings.ToLower(firstString(opts["orientation"]))
	if orientation == "" {
		orientation = "column"
	}
	cols := toStringList(opts["columns"])
	if len(cols) == 0 {
		cols = numericColumns(data)
	}
	if orientation == "row" {
		for _, row := range data {
			var total float64
			for _, col := range cols {
				if n, ok := numOf(row[col]); ok {
					total += n
				}
			}
			for _, col := range cols {
				if n, ok := numOf(row[col]); ok {
					if total == 0 {
						row[col] = nil
					} else {
						row[col] = roundTo(n/total*100, 4)
					}
				}
			}
		}
		return data, nil
	}

	for _, col := range cols {
		var total float64
		for _, row := range data {
			if n, ok := numOf(row[col]); ok {
				total += n
			}
		}
		for _, row := range data {
			if n, ok := numOf(row[col]); ok {
				if total == 0 {
					row[col] = nil
				} else {
					row[col] = roundTo(n/total*100, 4)
				}
			}
		}
	}
	return data, nil
}

// ppRank adds a "rank" column holding the percentile rank of the metric.
func ppRank(data []map[string]any, opts map[string]any) ([]map[string]any, error) {
	metric := firstString(opts["metric"])
	if metric == "" {
		return nil, fmt.Errorf("rank requires a metric")
	}
	groupBy := firstString(opts["group_by"])
	if groupBy == "" {
		ranks := percentileRanks(data, metric)
		for i, row := range data {
			row["rank"] = ranks[i]
		}
		return data, nil
	}

	groups := map[string][]int{}
	order := []string{}
	for i, row := range data {
		key := groupKey(row, []string{groupBy})
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], i)
	}
	for _, key := range order {
		idx := groups[key]
		subset := make([]map[string]any, 0, len(idx))
		for _, i := range idx {
			subset = append(subset, data[i])
		}
		ranks := percentileRanks(subset, metric)
		for j, i := range idx {
			data[i]["rank"] = ranks[j]
		}
	}
	return data, nil
}

// ppFlatten explodes list valued cells into multiple rows.
func ppFlatten(data []map[string]any, opts map[string]any) ([]map[string]any, error) {
	cols := toStringList(opts["columns"])
	if len(cols) == 0 {
		return nil, fmt.Errorf("flatten requires at least one column")
	}
	out := make([]map[string]any, 0, len(data))
	for _, row := range data {
		var lists [][]any
		maxLen := 1
		for _, col := range cols {
			items, ok := flattenList(row[col])
			if !ok {
				return nil, fmt.Errorf("column %q does not contain a list", col)
			}
			lists = append(lists, items)
			if len(items) > maxLen {
				maxLen = len(items)
			}
		}
		for i := 0; i < maxLen; i++ {
			clone := make(map[string]any, len(row))
			for k, v := range row {
				clone[k] = v
			}
			for ci, col := range cols {
				if i < len(lists[ci]) {
					clone[col] = lists[ci][i]
				} else {
					clone[col] = nil
				}
			}
			out = append(out, clone)
		}
	}
	return out, nil
}

// ppCompare computes the difference between a source and a comparison column.
func ppCompare(data []map[string]any, opts map[string]any) ([]map[string]any, error) {
	source := toStringList(opts["source_columns"])
	compare := toStringList(opts["compare_columns"])
	if len(source) != len(compare) || len(source) == 0 {
		return nil, fmt.Errorf("compare_columns must have the same length as source_columns")
	}
	compareType := strings.ToLower(firstString(opts["compare_type"]))
	if compareType == "" {
		compareType = "percentage"
	}
	switch compareType {
	case "difference", "percentage", "ratio":
	default:
		return nil, fmt.Errorf("compare_type must be difference, percentage or ratio")
	}
	precision := asInt(opts["precision"])
	if precision <= 0 {
		precision = 4
	}
	for i := range source {
		newCol := compareType + "__" + source[i] + "__" + compare[i]
		for _, row := range data {
			a, aok := numOf(row[source[i]])
			b, bok := numOf(row[compare[i]])
			if !aok || !bok {
				row[newCol] = nil
				continue
			}
			switch compareType {
			case "difference":
				row[newCol] = roundTo(a-b, precision)
			case "percentage":
				if b == 0 {
					row[newCol] = nil
				} else {
					row[newCol] = roundTo((a-b)/b, precision)
				}
			case "ratio":
				if b == 0 {
					row[newCol] = nil
				} else {
					row[newCol] = roundTo(a/b, precision)
				}
			}
		}
	}
	if asBool(opts["drop_original_columns"]) {
		for _, row := range data {
			for _, col := range source {
				delete(row, col)
			}
			for _, col := range compare {
				delete(row, col)
			}
		}
	}
	return data, nil
}

// ppResample buckets the time series and fills gaps.
func ppResample(data []map[string]any, opts map[string]any) ([]map[string]any, error) {
	rule := firstString(opts["rule"])
	if rule == "" {
		return nil, fmt.Errorf("resample requires a rule")
	}
	step, err := parseResampleRule(rule)
	if err != nil {
		return nil, err
	}
	timeCol := firstString(opts["time_column"])
	if timeCol == "" {
		timeCol = DTTMColumnAlias
		if len(data) > 0 {
			if _, ok := data[0][timeCol]; !ok {
				timeCol = findTimeColumn(data)
			}
		}
	}
	if timeCol == "" {
		return nil, fmt.Errorf("resample requires a time column")
	}

	method := strings.ToLower(firstString(opts["method"]))
	if method == "" {
		method = "asfreq"
	}
	cols := toStringList(opts["columns"])
	if len(cols) == 0 {
		cols = numericColumns(data)
	}

	type agg struct {
		sum   float64
		count int
	}
	buckets := map[int64]map[string]*agg{}
	var minB, maxB int64
	first := true
	for _, row := range data {
		ts, ok := parseTimeValue(row[timeCol])
		if !ok {
			continue
		}
		b := bucketStart(ts, step)
		key := b.Unix()
		if first {
			minB, maxB, first = key, key, false
		}
		if key < minB {
			minB = key
		}
		if key > maxB {
			maxB = key
		}
		bucket, ok := buckets[key]
		if !ok {
			bucket = map[string]*agg{}
			buckets[key] = bucket
		}
		for _, col := range cols {
			if n, ok := numOf(row[col]); ok {
				a, ok := bucket[col]
				if !ok {
					a = &agg{}
					bucket[col] = a
				}
				a.sum += n
				a.count++
			}
		}
	}
	if len(buckets) == 0 {
		return []map[string]any{}, nil
	}

	out := []map[string]any{}
	prev := map[string]any{}
	stepSeconds := int64(step.Seconds())
	for key := minB; key <= maxB; key += stepSeconds {
		row := map[string]any{timeCol: time.Unix(key, 0).UTC().Format(time.RFC3339)}
		bucket := buckets[key]
		for _, col := range cols {
			if bucket != nil {
				if a, ok := bucket[col]; ok && a.count > 0 {
					row[col] = a.sum / float64(a.count)
					continue
				}
			}
			switch method {
			case "ffill":
				row[col] = prev[col]
			case "asfreq":
				row[col] = opts["fill_value"]
			default:
				row[col] = nil
			}
		}
		prev = row
		out = append(out, row)
	}

	switch method {
	case "bfill":
		for _, col := range cols {
			var next any
			for i := len(out) - 1; i >= 0; i-- {
				if out[i][col] != nil {
					next = out[i][col]
				} else {
					out[i][col] = next
				}
			}
		}
	case "linear":
		for _, col := range cols {
			interpolateColumn(out, col)
		}
	}
	return out, nil
}

// interpolateColumn fills nil cells by linear interpolation between the
// surrounding known values.
func interpolateColumn(rows []map[string]any, col string) {
	known := make([]int, 0)
	for i, row := range rows {
		if _, ok := numOf(row[col]); ok && row[col] != nil {
			known = append(known, i)
		}
	}
	for k := 0; k+1 < len(known); k++ {
		i, j := known[k], known[k+1]
		if j-i <= 1 {
			continue
		}
		a, _ := numOf(rows[i][col])
		b, _ := numOf(rows[j][col])
		for m := i + 1; m < j; m++ {
			ratio := float64(m-i) / float64(j-i)
			rows[m][col] = a + (b-a)*ratio
		}
	}
}

// ppTimeCompare joins the value of a previous period onto each row.
func ppTimeCompare(data []map[string]any, opts map[string]any) ([]map[string]any, error) {
	offsets := toStringList(opts["time_offsets"])
	if len(offsets) == 0 {
		return nil, fmt.Errorf("time_compare requires time_offsets")
	}
	timeCol := firstString(opts["time_column"])
	if timeCol == "" {
		timeCol = findTimeColumn(data)
	}
	if timeCol == "" {
		return nil, fmt.Errorf("time_compare requires a time column")
	}
	cols := toStringList(opts["columns"])
	if len(cols) == 0 {
		cols = numericColumns(data)
	}

	lookup := map[string]map[string]any{}
	for _, row := range data {
		if ts, ok := parseTimeValue(row[timeCol]); ok {
			lookup[ts.UTC().Format(time.RFC3339)] = row
		}
	}
	for _, offset := range offsets {
		for _, row := range data {
			ts, ok := parseTimeValue(row[timeCol])
			if !ok {
				continue
			}
			prevTS, ok := shiftTimestamp(ts, offset)
			if !ok {
				continue
			}
			source, ok := lookup[prevTS.UTC().Format(time.RFC3339)]
			if !ok {
				continue
			}
			for _, col := range cols {
				row[col+"__"+offset] = source[col]
			}
		}
	}
	return data, nil
}

// ppGeohashDecode decodes geohash strings into latitude/longitude columns.
func ppGeohashDecode(data []map[string]any, opts map[string]any) ([]map[string]any, error) {
	cols := toStringList(opts["columns"])
	if len(cols) == 0 {
		return nil, fmt.Errorf("geohash_decode requires at least one column")
	}
	for _, col := range cols {
		latCol := col + "_latitude"
		lngCol := col + "_longitude"
		for _, row := range data {
			hash := firstString(row[col])
			if hash == "" {
				row[latCol] = nil
				row[lngCol] = nil
				continue
			}
			lat, lng, err := decodeGeohash(hash)
			if err != nil {
				row[latCol] = nil
				row[lngCol] = nil
				continue
			}
			row[latCol] = lat
			row[lngCol] = lng
		}
	}
	return data, nil
}

// Helpers ------------------------------------------------------------------

func aggregateSpec(opts map[string]any) map[string]string {
	out := map[string]string{}
	for _, key := range []string{"aggregates", "aggregate", "columns"} {
		raw := asStringMap(opts[key])
		for col, spec := range raw {
			fn := ""
			switch v := spec.(type) {
			case string:
				fn = v
			case map[string]any:
				fn = firstString(v["aggregateFunction"], v["aggregate_function"], v["func"], v["function"])
			}
			if fn == "" {
				fn = "sum"
			}
			out[col] = strings.ToLower(fn)
		}
	}
	return out
}

func aggregateValues(values []float64, fn string) (any, error) {
	if len(values) == 0 {
		return nil, nil
	}
	switch strings.ToLower(fn) {
	case "sum":
		var total float64
		for _, v := range values {
			total += v
		}
		return total, nil
	case "mean", "avg", "average":
		var total float64
		for _, v := range values {
			total += v
		}
		return total / float64(len(values)), nil
	case "min":
		m := values[0]
		for _, v := range values[1:] {
			if v < m {
				m = v
			}
		}
		return m, nil
	case "max":
		m := values[0]
		for _, v := range values[1:] {
			if v > m {
				m = v
			}
		}
		return m, nil
	case "count":
		return len(values), nil
	case "median":
		cp := make([]float64, len(values))
		copy(cp, values)
		sort.Float64s(cp)
		mid := len(cp) / 2
		if len(cp)%2 == 0 {
			return (cp[mid-1] + cp[mid]) / 2, nil
		}
		return cp[mid], nil
	case "std", "stddev", "stddev_samp":
		if len(values) < 2 {
			return nil, nil
		}
		mean, _ := aggregateValues(values, "mean")
		m := mean.(float64)
		var variance float64
		for _, v := range values {
			variance += (v - m) * (v - m)
		}
		return math.Sqrt(variance / float64(len(values)-1)), nil
	case "var", "variance":
		if len(values) < 2 {
			return nil, nil
		}
		mean, _ := aggregateValues(values, "mean")
		m := mean.(float64)
		var variance float64
		for _, v := range values {
			variance += (v - m) * (v - m)
		}
		return variance / float64(len(values)-1), nil
	case "first":
		return values[0], nil
	case "last":
		return values[len(values)-1], nil
	}
	return nil, fmt.Errorf("unsupported aggregate function %q", fn)
}

func percentileRanks(rows []map[string]any, col string) []any {
	type pair struct {
		idx int
		val float64
	}
	pairs := make([]pair, 0, len(rows))
	for i, row := range rows {
		if n, ok := numOf(row[col]); ok {
			pairs = append(pairs, pair{idx: i, val: n})
		}
	}
	out := make([]any, len(rows))
	if len(pairs) == 0 {
		return out
	}
	sort.SliceStable(pairs, func(i, j int) bool { return pairs[i].val < pairs[j].val })
	ranks := make([]float64, len(pairs))
	i := 0
	for i < len(pairs) {
		j := i
		for j+1 < len(pairs) && pairs[j+1].val == pairs[i].val {
			j++
		}
		avgRank := float64(i+j)/2 + 1
		for k := i; k <= j; k++ {
			ranks[k] = avgRank
		}
		i = j + 1
	}
	for i, p := range pairs {
		out[p.idx] = ranks[i] / float64(len(pairs))
	}
	return out
}

func groupKey(row map[string]any, cols []string) string {
	if len(cols) == 0 {
		return "__all__"
	}
	parts := make([]string, 0, len(cols))
	for _, c := range cols {
		parts = append(parts, fmt.Sprint(row[c]))
	}
	return strings.Join(parts, "\x1f")
}

func numOf(v any) (float64, bool) {
	switch t := v.(type) {
	case nil:
		return 0, false
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int32:
		return float64(t), true
	case int64:
		return float64(t), true
	case uint:
		return float64(t), true
	case uint64:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return f, err == nil
	case bool:
		if t {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

func compareValues(a, b any) int {
	an, aok := numOf(a)
	bn, bok := numOf(b)
	if aok && bok {
		switch {
		case an < bn:
			return -1
		case an > bn:
			return 1
		}
		return 0
	}
	as := fmt.Sprint(a)
	bs := fmt.Sprint(b)
	return strings.Compare(as, bs)
}

func toStringList(v any) []string {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		if strings.TrimSpace(t) == "" {
			return nil
		}
		return []string{strings.TrimSpace(t)}
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s := firstString(item); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return t
	}
	return nil
}

func toBoolList(v any, n int) []bool {
	out := make([]bool, 0, n)
	switch t := v.(type) {
	case bool:
		for i := 0; i < n; i++ {
			out = append(out, t)
		}
	case []any:
		for _, item := range t {
			out = append(out, asBool(item))
		}
	}
	if len(out) == 0 {
		for i := 0; i < n; i++ {
			out = append(out, true)
		}
	}
	return out
}

func numericColumns(data []map[string]any) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, row := range data {
		for k, v := range row {
			if seen[k] {
				continue
			}
			if _, ok := numOf(v); ok {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sort.Strings(out)
	return out
}

func flattenList(v any) ([]any, bool) {
	switch t := v.(type) {
	case []any:
		return t, true
	case []string:
		out := make([]any, 0, len(t))
		for _, s := range t {
			out = append(out, s)
		}
		return out, true
	case string:
		trimmed := strings.TrimSpace(t)
		if strings.HasPrefix(trimmed, "[") {
			var out []any
			if err := json.Unmarshal([]byte(trimmed), &out); err == nil {
				return out, true
			}
		}
		return nil, false
	}
	return nil, false
}

func roundTo(v float64, precision int) float64 {
	factor := math.Pow(10, float64(precision))
	return math.Round(v*factor) / factor
}

func findTimeColumn(data []map[string]any) string {
	for _, row := range data {
		for k, v := range row {
			if _, ok := parseTimeValue(v); ok {
				return k
			}
		}
	}
	return ""
}

func parseTimeValue(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t, true
	case string:
		for _, layout := range []string{
			time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02",
		} {
			if parsed, err := time.Parse(layout, t); err == nil {
				return parsed, true
			}
		}
	case int64:
		return time.Unix(t, 0), true
	case float64:
		return time.Unix(int64(t), 0), true
	}
	return time.Time{}, false
}

// parseResampleRule accepts ISO-8601 durations (P1D, PT1H) and pandas offsets
// (1D, 1H, 1M).
func parseResampleRule(rule string) (time.Duration, error) {
	upper := strings.ToUpper(strings.TrimSpace(rule))
	upper = strings.TrimPrefix(upper, "P")
	upper = strings.TrimPrefix(upper, "T")
	idx := 0
	for idx < len(upper) && upper[idx] >= '0' && upper[idx] <= '9' {
		idx++
	}
	if idx == 0 {
		return 0, fmt.Errorf("invalid resample rule %q", rule)
	}
	n, err := strconv.Atoi(upper[:idx])
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid resample rule %q", rule)
	}
	unit := upper[idx:]
	switch unit {
	case "S":
		return time.Duration(n) * time.Second, nil
	case "MIN", "MINS", "MINUTE":
		return time.Duration(n) * time.Minute, nil
	case "H", "HOUR":
		return time.Duration(n) * time.Hour, nil
	case "D", "DAY":
		return time.Duration(n) * 24 * time.Hour, nil
	case "W", "WEEK":
		return time.Duration(n) * 7 * 24 * time.Hour, nil
	case "M", "MON", "MONTH":
		return time.Duration(n) * 30 * 24 * time.Hour, nil
	case "Q", "QUARTER":
		return time.Duration(n) * 90 * 24 * time.Hour, nil
	case "Y", "YEAR":
		return time.Duration(n) * 365 * 24 * time.Hour, nil
	}
	return 0, fmt.Errorf("invalid resample rule %q", rule)
}

func bucketStart(t time.Time, step time.Duration) time.Time {
	utc := t.UTC()
	epoch := utc.Unix()
	seconds := int64(step.Seconds())
	if seconds <= 0 {
		return utc
	}
	return time.Unix(epoch-epoch%seconds, 0).UTC()
}

func shiftTimestamp(ts time.Time, offset string) (time.Time, bool) {
	m := timeShiftPattern.FindStringSubmatch(strings.ToLower(strings.TrimSpace(offset)))
	if m == nil {
		return time.Time{}, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return time.Time{}, false
	}
	if m[3] == "ago" {
		n = -n
	}
	return shiftTime(ts, m[2], n), true
}

// decodeGeohash decodes a geohash string into lat/lng (WGS84).
func decodeGeohash(hash string) (float64, float64, error) {
	const alphabet = "0123456789bcdefghjkmnpqrstuvwxyz"
	latLo, latHi := -90.0, 90.0
	lngLo, lngHi := -180.0, 180.0
	even := true
	for _, r := range strings.ToLower(hash) {
		idx := strings.IndexRune(alphabet, r)
		if idx < 0 {
			return 0, 0, fmt.Errorf("invalid geohash %q", hash)
		}
		for mask := 16; mask > 0; mask >>= 1 {
			bit := idx&mask != 0
			if even {
				mid := (lngLo + lngHi) / 2
				if bit {
					lngLo = mid
				} else {
					lngHi = mid
				}
			} else {
				mid := (latLo + latHi) / 2
				if bit {
					latLo = mid
				} else {
					latHi = mid
				}
			}
			even = !even
		}
	}
	return (latLo + latHi) / 2, (lngLo + lngHi) / 2, nil
}
