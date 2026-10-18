package examples

import "strconv"

// chartDef is one demo chart. params is a JSON template whose single %d is
// replaced with the dataset id at load time, because the id depends on what
// else the installation already holds.
type chartDef struct {
	name        string
	vizType     string
	description string
	params      string
}

// chartDefs mirrors the six charts upstream Superset ships for birth_names.
// The parameter sets are the ones the live instance renders, so every chart
// keeps working without tuning.
func chartDefs() []chartDef {
	return []chartDef{
		{
			name:        "Births by Gender per Month",
			vizType:     "bar",
			description: "Monthly birth counts split by gender",
			params: `{"adhoc_filters":[],"color_scheme":"supersetColors","datasource":"%d__table",` +
				`"granularity_sqla":"ds","groupby":["gender"],"metrics":["count"],"order_desc":true,` +
				`"rich_tooltip":true,"row_limit":100,"show_legend":true,"time_grain_sqla":"P1M",` +
				`"time_range":"2018-01-01 : 2019-01-01","viz_type":"bar","x_axis":"__timestamp",` +
				`"y_axis_format":"SMART_NUMBER"}`,
		},
		{
			name:    "Monthly Births (Area)",
			vizType: "echarts_timeseries_area",
			params: `{"datasource":"%d__table","granularity_sqla":"ds","groupby":["gender"],` +
				`"metrics":["count"],"row_limit":100,"time_grain_sqla":"P1M",` +
				`"time_range":"2018-01-01 : 2019-01-01","viz_type":"echarts_timeseries_area"}`,
		},
		{
			name:    "Births by Gender (Pie)",
			vizType: "pie",
			params: `{"datasource":"%d__table","granularity_sqla":"ds","groupby":["gender"],` +
				`"metrics":["count"],"row_limit":100,"time_grain_sqla":"P1M",` +
				`"time_range":"2018-01-01 : 2019-01-01","viz_type":"pie"}`,
		},
		{
			name:    "Total Births (Big Number)",
			vizType: "big_number",
			params: `{"datasource":"%d__table","granularity_sqla":"ds","groupby":[],` +
				`"metrics":["count"],"row_limit":100,` +
				`"time_range":"2018-01-01 : 2019-01-01","viz_type":"big_number"}`,
		},
		{
			name:    "Births by State (Funnel)",
			vizType: "funnel",
			params: `{"datasource":"%d__table","granularity_sqla":"ds","groupby":["state"],` +
				`"metrics":["count"],"row_limit":20,` +
				`"time_range":"2018-01-01 : 2019-01-01","viz_type":"funnel"}`,
		},
		{
			name:    "Births by State and Gender (Heatmap)",
			vizType: "heatmap",
			params: `{"datasource":"%d__table","granularity_sqla":"ds",` +
				`"groupby":["state","gender"],"metrics":["count"],"row_limit":200,` +
				`"time_range":"2018-01-01 : 2019-01-01","viz_type":"heatmap"}`,
		},
	}
}

// dashboardLayout groups the chart names row by row. A name that is missing
// from the installation is simply skipped, so a partially edited dashboard
// still gets a valid grid.
var dashboardLayout = [][]string{
	{"Births by Gender per Month", "Monthly Births (Area)"},
	{"Births by Gender (Pie)", "Total Births (Big Number)"},
	{"Births by State (Funnel)", "Births by State and Gender (Heatmap)"},
}

// dashboardPositions renders the Superset position_json document for the
// overview dashboard.
func dashboardPositions(byName map[string]int) map[string]any {
	rows := make([]string, 0, len(dashboardLayout))
	positions := map[string]any{
		"DASHBOARD_VERSION_KEY": "v2",
		"ROOT_ID": map[string]any{
			"id": "ROOT_ID", "type": "ROOT", "children": []string{"GRID_ID"}, "parents": []string{},
		},
		"HEADER_ID": map[string]any{
			"id": "HEADER_ID", "type": "HEADER",
			"meta": map[string]any{"text": dashboardTitle, "headerSize": "LARGE_HEADER"},
		},
	}

	for i, row := range dashboardLayout {
		rowID := "ROW-" + strconv.Itoa(i+1)
		children := make([]string, 0, len(row))
		for j, name := range row {
			chartID, ok := byName[name]
			if !ok {
				continue
			}
			key := "CHART-" + strconv.Itoa(i+1) + "-" + strconv.Itoa(j+1)
			children = append(children, key)
			positions[key] = map[string]any{
				"id": key, "type": "CHART", "children": []string{},
				"parents": []string{"ROOT_ID", "GRID_ID", rowID},
				"meta": map[string]any{
					"chartId": chartID, "sliceName": name, "width": 6, "height": 50,
				},
			}
		}
		if len(children) == 0 {
			continue
		}
		rows = append(rows, rowID)
		positions[rowID] = map[string]any{
			"id": rowID, "type": "ROW", "children": children,
			"parents": []string{"ROOT_ID", "GRID_ID"},
			"meta":    map[string]any{"background": "BACKGROUND_TRANSPARENT"},
		}
	}

	positions["GRID_ID"] = map[string]any{
		"id": "GRID_ID", "type": "GRID", "children": rows, "parents": []string{"ROOT_ID"},
	}
	return positions
}

// dashboardMetadata carries the cross-filter scope and the gender native
// filter, so the demo dashboard shows the filtering story and not just tiles.
func dashboardMetadata(datasetID int) map[string]any {
	return map[string]any{
		"chart_configuration":   map[string]any{},
		"chartsInScope":         []int{},
		"color_scheme":          "supersetColors",
		"cross_filters_enabled": true,
		"default_filters":       "{}",
		"expanded_slices":       map[string]any{},
		"filter_scopes":         "{}",
		"global_chart_configuration": map[string]any{
			"globalChartConfigList": []any{},
			"scope": map[string]any{
				"excluded": []any{}, "rootPath": []string{"ROOT_ID"},
			},
		},
		"native_filter_configuration": []any{
			map[string]any{
				"id":          "NATIVE_FILTER-gender",
				"name":        "Gender",
				"filterType":  "filter_select",
				"type":        "NATIVE_FILTER",
				"description": "Filters every tile that reads the birth_names dataset",
				"targets": []any{
					map[string]any{
						"datasetId": datasetID,
						"column":    map[string]any{"name": "gender"},
					},
				},
				"controlValues": map[string]any{
					"multiSelection": false, "defaultToFirstItem": false, "sortAscending": true,
				},
				"defaultDataMask": map[string]any{
					"filterState": map[string]any{"value": nil},
				},
				"requiredFirst": false,
			},
		},
		"refresh_frequency":           0,
		"timed_refresh_immune_slices": []any{},
	}
}
