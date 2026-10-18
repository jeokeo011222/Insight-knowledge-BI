package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"

	"superset/internal/models"
	apperrors "superset/internal/pkg/errors"
	"superset/internal/security"
	"superset/internal/server"
)

// RegisterTags mounts /api/v1/tag/* mirroring superset/tags/api.py.
func RegisterTags(r chi.Router, deps *Deps) {
	t := &tagAPI{deps: deps}

	r.Route("/tag", func(tr chi.Router) {
		tr.Use(security.RequireUser)

		tr.With(security.RequirePermission(security.ActionRead, security.ResTag)).Get("/", t.list)
		tr.With(security.RequirePermission(security.ActionWrite, security.ResTag)).Post("/", t.create)

		tr.With(security.RequirePermission(security.ActionRead, security.ResTag)).Get("/{object_type}/", t.listByType)
		tr.With(security.RequirePermission(security.ActionRead, security.ResTag)).Get("/{object_type}/{object_id}", t.listForObject)
		tr.With(security.RequirePermission(security.ActionWrite, security.ResTag)).Post("/{object_type}/{object_id}", t.tagObject)
		tr.With(security.RequirePermission(security.ActionWrite, security.ResTag)).
			Delete("/{object_type}/{object_id}/{tag_id}", t.untagObject)
	})
}

type tagAPI struct{ deps *Deps }

// Tag types (superset.tags.models.TagType).
const (
	tagTypeCustom      = 1
	tagTypeImplicit    = 2
	tagTypeEditor      = 3
	tagTypeFavoritedBy = 4
)

// Object types (superset.tags.models.ObjectType).
const (
	tagObjectQuery     = 1
	tagObjectChart     = 2
	tagObjectDashboard = 3
	tagObjectDataset   = 4
)

// tagObjectType resolves an object type name or numeric value.
func tagObjectType(raw string) (int, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "query":
		return tagObjectQuery, true
	case "chart":
		return tagObjectChart, true
	case "dashboard":
		return tagObjectDashboard, true
	case "dataset", "table":
		return tagObjectDataset, true
	}
	if n, err := strconv.Atoi(raw); err == nil && n >= tagObjectQuery && n <= tagObjectDataset {
		return n, true
	}
	return 0, false
}

// tagTypeName renders the enum value the way the Python schema does.
func tagTypeName(v int) string {
	switch v {
	case tagTypeCustom:
		return "custom"
	case tagTypeImplicit:
		return "type"
	case tagTypeEditor:
		return "editor"
	case tagTypeFavoritedBy:
		return "favorited_by"
	default:
		return "custom"
	}
}

func tagsJSON(t *models.Tag) map[string]any {
	return map[string]any{
		"id": t.ID, "name": t.Name, "type": tagTypeName(t.Type),
		"description": nullIfEmpty(t.Description),
		"created_on":  t.CreatedOn, "changed_on": t.ChangedOn,
	}
}

type tagsCreatePayload struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type tagsAttachPayload struct {
	Tags []json.RawMessage `json:"tags"`
}

func (t *tagAPI) list(w http.ResponseWriter, r *http.Request) {
	p := parseListParams(r)
	q := t.deps.DB.WithContext(r.Context()).Model(&models.Tag{})
	if p.Search != "" {
		like := "%" + p.Search + "%"
		q = q.Where("name ILIKE ? OR description ILIKE ?", like, like)
	}
	q, filtersOK := applyListFilters(w, q, p)
	if !filtersOK {
		return
	}
	listResultW(w, r, q, p, "name asc", func(x *models.Tag) any { return tagsJSON(x) })
}

func (t *tagAPI) create(w http.ResponseWriter, r *http.Request) {
	var p tagsCreatePayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	tag, err := t.ensureTag(r, p.Name, p.Description)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	server.WriteJSON(w, http.StatusCreated, map[string]any{"result": tagsJSON(tag)})
}

// ensureTag returns the tag with the given name, creating it when missing.
func (t *tagAPI) ensureTag(r *http.Request, name, description string) (*models.Tag, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, apperrors.BadRequest("Tag name is required")
	}
	db := t.deps.DB.WithContext(r.Context())
	var tag models.Tag
	err := db.Where("name = ?", name).First(&tag).Error
	if err == nil {
		if description != "" && tag.Description == "" {
			db.Model(&tag).Update("description", description)
			tag.Description = description
		}
		return &tag, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, apperrors.Internal("Database error: %s", err.Error())
	}
	tag = models.Tag{Name: name, Type: tagTypeCustom, Description: description}
	if err := db.Create(&tag).Error; err != nil {
		return nil, apperrors.Conflict("Could not create tag: %s", err.Error())
	}
	return &tag, nil
}

func (t *tagAPI) listByType(w http.ResponseWriter, r *http.Request) {
	objectType, ok := tagObjectType(server.URLParam(r, "object_type"))
	if !ok {
		server.WriteError(w, apperrors.BadRequest("Invalid object type"))
		return
	}
	var tags []models.Tag
	if err := t.deps.DB.WithContext(r.Context()).
		Table("tag").
		Select("tag.*").
		Joins("JOIN tagged_object ON tagged_object.tag_id = tag.id").
		Where("tagged_object.object_type = ?", objectType).
		Group("tag.id, tag.name, tag.type, tag.description, tag.created_on, tag.changed_on").
		Order("tag.name asc").
		Find(&tags).Error; err != nil {
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
		return
	}
	out := make([]map[string]any, 0, len(tags))
	for i := range tags {
		out = append(out, tagsJSON(&tags[i]))
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": out, "count": len(out)})
}

func (t *tagAPI) listForObject(w http.ResponseWriter, r *http.Request) {
	objectType, ok := tagObjectType(server.URLParam(r, "object_type"))
	if !ok {
		server.WriteError(w, apperrors.BadRequest("Invalid object type"))
		return
	}
	objectID, err := server.URLParamInt(r, "object_id")
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var tags []models.Tag
	if err := t.deps.DB.WithContext(r.Context()).
		Table("tag").
		Select("tag.*").
		Joins("JOIN tagged_object ON tagged_object.tag_id = tag.id").
		Where("tagged_object.object_type = ? AND tagged_object.object_id = ?", objectType, objectID).
		Order("tag.name asc").
		Find(&tags).Error; err != nil {
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
		return
	}
	out := make([]map[string]any, 0, len(tags))
	for i := range tags {
		out = append(out, tagsJSON(&tags[i]))
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{
		"result": out, "count": len(out),
		"object_type": server.URLParam(r, "object_type"), "object_id": objectID,
	})
}

func (t *tagAPI) tagObject(w http.ResponseWriter, r *http.Request) {
	objectType, ok := tagObjectType(server.URLParam(r, "object_type"))
	if !ok {
		server.WriteError(w, apperrors.BadRequest("Invalid object type"))
		return
	}
	objectID, err := server.URLParamInt(r, "object_id")
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var p tagsAttachPayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	if len(p.Tags) == 0 {
		server.WriteError(w, apperrors.BadRequest("tags is required"))
		return
	}

	attached := make([]map[string]any, 0, len(p.Tags))
	for _, raw := range p.Tags {
		name, description := tagsParseRef(raw)
		if name == "" {
			server.WriteError(w, apperrors.BadRequest("Each tag requires a name"))
			return
		}
		tag, err := t.ensureTag(r, name, description)
		if err != nil {
			server.WriteError(w, err)
			return
		}
		db := t.deps.DB.WithContext(r.Context())
		var link models.TaggedObject
		err = db.Where("tag_id = ? AND object_id = ? AND object_type = ?", tag.ID, objectID, objectType).
			First(&link).Error
		if err == gorm.ErrRecordNotFound {
			link = models.TaggedObject{TagID: tag.ID, ObjectID: objectID, ObjectType: objectType}
			if err := db.Create(&link).Error; err != nil {
				server.WriteError(w, apperrors.Conflict("Could not tag object: %s", err.Error()))
				return
			}
		} else if err != nil {
			server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
			return
		}
		attached = append(attached, tagsJSON(tag))
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"message": "OK", "result": attached})
}

// tagsParseRef accepts either "name" or {"name": ..., "description": ...}.
func tagsParseRef(raw json.RawMessage) (name, description string) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s), ""
	}
	var obj struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return strings.TrimSpace(obj.Name), obj.Description
	}
	return "", ""
}

func (t *tagAPI) untagObject(w http.ResponseWriter, r *http.Request) {
	objectType, ok := tagObjectType(server.URLParam(r, "object_type"))
	if !ok {
		server.WriteError(w, apperrors.BadRequest("Invalid object type"))
		return
	}
	objectID, err := server.URLParamInt(r, "object_id")
	if err != nil {
		server.WriteError(w, err)
		return
	}
	tagID, err := server.URLParamInt(r, "tag_id")
	if err != nil {
		server.WriteError(w, err)
		return
	}
	res := t.deps.DB.WithContext(r.Context()).
		Where("tag_id = ? AND object_id = ? AND object_type = ?", tagID, objectID, objectType).
		Delete(&models.TaggedObject{})
	if res.Error != nil {
		server.WriteError(w, apperrors.Internal("Database error: %s", res.Error.Error()))
		return
	}
	if res.RowsAffected == 0 {
		server.WriteError(w, apperrors.NotFound("Tagged object not found"))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"message": "OK"})
}
