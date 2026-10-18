package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"superset/internal/models"
	apperrors "superset/internal/pkg/errors"
	"superset/internal/security"
	"superset/internal/server"
)

// Lineage: dataset → chart → dashboard, the two hops our metadata already
// models. Surfacing them answers the question every governance review asks
// ("what breaks if I change this column?") without introducing a catalog.
//
// Registered as GET /lineage/{type}/{id} where type is dataset|chart|dashboard.

// RegisterLineage mounts /api/v1/lineage/*.
func RegisterLineage(r chi.Router, deps *Deps) {
	l := &lineageAPI{deps: deps}
	r.Route("/lineage", func(lr chi.Router) {
		lr.Use(security.RequireUser)
		// Single parameterised route: chi exposes the captured segment through
		// URLParam("type"), which static aliases such as /dataset/{pk} do not.
		lr.With(security.RequirePermission(security.ActionRead, security.ResDataset)).
			Get("/{type}/{pk}", l.get)
	})
}

type lineageAPI struct{ deps *Deps }

// lineageNode is one vertex of the graph.
type lineageNode struct {
	Key      string `json:"key"`
	Type     string `json:"type"`
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Subtitle string `json:"subtitle,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

// lineageEdge is one directed dependency (source depends on target).
type lineageEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

type lineageGraph struct {
	Nodes []lineageNode `json:"nodes"`
	Edges []lineageEdge `json:"edges"`
	// Upstream = what this object is built from, downstream = what uses it.
	Upstream   []lineageNode `json:"upstream"`
	Downstream []lineageNode `json:"downstream"`
}

func nodeKey(kind string, id int) string {
	return kind + ":" + strconv.Itoa(id)
}

func (l *lineageAPI) get(w http.ResponseWriter, r *http.Request) {
	kind := chi.URLParam(r, "type")
	pk, err := strconv.Atoi(chi.URLParam(r, "pk"))
	if err != nil || pk <= 0 {
		server.WriteError(w, apperrors.NotFound("Object not found"))
		return
	}
	ctx := r.Context()
	db := l.deps.DB.WithContext(ctx)

	graph := lineageGraph{Nodes: []lineageNode{}, Edges: []lineageEdge{},
		Upstream: []lineageNode{}, Downstream: []lineageNode{}}
	seen := map[string]bool{}
	addNode := func(n lineageNode) {
		if seen[n.Key] {
			return
		}
		seen[n.Key] = true
		graph.Nodes = append(graph.Nodes, n)
	}
	addEdge := func(source, target string) {
		for _, e := range graph.Edges {
			if e.Source == source && e.Target == target {
				return
			}
		}
		graph.Edges = append(graph.Edges, lineageEdge{Source: source, Target: target})
	}

	var root lineageNode
	switch kind {
	case "dataset":
		var ds models.SqlaTable
		if err := db.First(&ds, pk).Error; err != nil {
			server.WriteError(w, apperrors.NotFound("Dataset not found"))
			return
		}
		root = lineageNode{Key: nodeKey("dataset", ds.ID), Type: "dataset", ID: ds.ID,
			Name: ds.TableNameStr, Subtitle: ds.Schema, Detail: "dataset"}
	case "chart":
		var s models.Slice
		if err := db.First(&s, pk).Error; err != nil {
			server.WriteError(w, apperrors.NotFound("Chart not found"))
			return
		}
		root = lineageNode{Key: nodeKey("chart", s.ID), Type: "chart", ID: s.ID,
			Name: s.SliceName, Subtitle: s.VizType, Detail: "chart"}
	case "dashboard":
		var dm models.Dashboard
		if err := db.First(&dm, pk).Error; err != nil {
			server.WriteError(w, apperrors.NotFound("Dashboard not found"))
			return
		}
		root = lineageNode{Key: nodeKey("dashboard", dm.ID), Type: "dashboard", ID: dm.ID,
			Name: dm.DashboardTitle, Subtitle: dm.Slug, Detail: "dashboard"}
	default:
		server.WriteError(w, apperrors.BadRequest("Unsupported lineage type: %s", kind))
		return
	}
	addNode(root)

	// chartsOfDatasets resolves every chart bound to a set of datasets.
	chartsOfDatasets := func(datasetIDs []int) []models.Slice {
		if len(datasetIDs) == 0 {
			return nil
		}
		var slices []models.Slice
		_ = db.Where("datasource_id IN ? AND datasource_type = ? AND deleted_at IS NULL",
			datasetIDs, "table").Find(&slices).Error
		return slices
	}
	// dashboardsOfCharts resolves every dashboard containing a set of charts.
	dashboardsOfCharts := func(chartIDs []int) []models.Dashboard {
		if len(chartIDs) == 0 {
			return nil
		}
		var list []models.Dashboard
		_ = db.Raw(`
			SELECT d.* FROM dashboards d
			JOIN dashboard_slices ds ON ds.dashboard_id = d.id
			WHERE ds.slice_id IN ? AND d.deleted_at IS NULL
			GROUP BY d.id
			ORDER BY d.dashboard_title`, chartIDs).Scan(&list).Error
		return list
	}
	datasetNodes := func(ids []int) []models.SqlaTable {
		if len(ids) == 0 {
			return nil
		}
		var list []models.SqlaTable
		_ = db.Where("id IN ?", ids).Find(&list).Error
		return list
	}
	appendChart := func(s models.Slice) lineageNode {
		n := lineageNode{Key: nodeKey("chart", s.ID), Type: "chart", ID: s.ID,
			Name: s.SliceName, Subtitle: s.VizType, Detail: "chart"}
		addNode(n)
		return n
	}
	appendDashboard := func(dm models.Dashboard) lineageNode {
		n := lineageNode{Key: nodeKey("dashboard", dm.ID), Type: "dashboard", ID: dm.ID,
			Name: dm.DashboardTitle, Subtitle: dm.Slug, Detail: "dashboard"}
		addNode(n)
		return n
	}
	appendDataset := func(ds models.SqlaTable) lineageNode {
		n := lineageNode{Key: nodeKey("dataset", ds.ID), Type: "dataset", ID: ds.ID,
			Name: ds.TableNameStr, Subtitle: ds.Schema, Detail: "dataset"}
		addNode(n)
		return n
	}

	switch kind {
	case "dataset":
		charts := chartsOfDatasets([]int{pk})
		chartIDs := make([]int, 0, len(charts))
		for _, s := range charts {
			chartIDs = append(chartIDs, s.ID)
			n := appendChart(s)
			graph.Downstream = append(graph.Downstream, n)
			addEdge(n.Key, root.Key) // chart depends on dataset
		}
		for _, dm := range dashboardsOfCharts(chartIDs) {
			graph.Downstream = append(graph.Downstream, appendDashboard(dm))
		}
		// Connect dashboards to the charts they contain.
		if len(chartIDs) > 0 {
			var links []models.DashboardSlices
			_ = db.Where("slice_id IN ?", chartIDs).Find(&links).Error
			for _, link := range links {
				addEdge(nodeKey("dashboard", link.DashboardID), nodeKey("chart", link.SliceID))
			}
		}

	case "chart":
		var s models.Slice
		if err := db.First(&s, pk).Error; err == nil && s.DatasourceType == "table" && s.DatasourceID > 0 {
			for _, ds := range datasetNodes([]int{s.DatasourceID}) {
				n := appendDataset(ds)
				graph.Upstream = append(graph.Upstream, n)
				addEdge(root.Key, n.Key)
			}
		}
		for _, dm := range dashboardsOfCharts([]int{pk}) {
			n := appendDashboard(dm)
			graph.Downstream = append(graph.Downstream, n)
			addEdge(n.Key, root.Key)
		}

	case "dashboard":
		var links []models.DashboardSlices
		_ = db.Where("dashboard_id = ?", pk).Find(&links).Error
		chartIDs := make([]int, 0, len(links))
		for _, link := range links {
			chartIDs = append(chartIDs, link.SliceID)
		}
		var slices []models.Slice
		if len(chartIDs) > 0 {
			_ = db.Where("id IN ? AND deleted_at IS NULL", chartIDs).Find(&slices).Error
		}
		datasetIDs := make([]int, 0, len(slices))
		for _, s := range slices {
			n := appendChart(s)
			graph.Upstream = append(graph.Upstream, n)
			addEdge(root.Key, n.Key)
			if s.DatasourceType == "table" && s.DatasourceID > 0 {
				datasetIDs = append(datasetIDs, s.DatasourceID)
			}
		}
		for _, ds := range datasetNodes(datasetIDs) {
			n := appendDataset(ds)
			graph.Upstream = append(graph.Upstream, n)
			// The dataset feeds every chart in the dashboard that uses it.
			for _, s := range slices {
				if s.DatasourceType == "table" && s.DatasourceID == ds.ID {
					addEdge(nodeKey("chart", s.ID), n.Key)
				}
			}
		}
	}

	// Drop the self edges the dataset branch can produce while wiring
	// dashboards to charts.
	cleaned := make([]lineageEdge, 0, len(graph.Edges))
	for _, e := range graph.Edges {
		if e.Source == e.Target || e.Source == "" || e.Target == "" {
			continue
		}
		cleaned = append(cleaned, e)
	}
	graph.Edges = cleaned

	server.WriteJSON(w, http.StatusOK, map[string]any{"result": graph})
}
