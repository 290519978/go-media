package server

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"maas-box/internal/model"
)

type areaUpsertRequest struct {
	Name     string `json:"name"`
	ParentID string `json:"parent_id"`
	Sort     int    `json:"sort"`
}

type areaNode struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	ParentID string     `json:"parent_id"`
	IsRoot   bool       `json:"is_root"`
	Sort     int        `json:"sort"`
	Children []areaNode `json:"children"`
}

func (s *Server) registerAreaRoutes(r gin.IRouter) {
	g := r.Group("/areas")
	g.GET("", s.listAreas)
	g.POST("", s.createArea)
	g.PUT("/:id", s.updateArea)
	g.DELETE("/:id", s.deleteArea)
}

func (s *Server) listAreas(c *gin.Context) {
	var areas []model.Area
	if err := s.db.Order("sort asc, created_at asc").Find(&areas).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "query areas failed")
		return
	}

	children := make(map[string][]model.Area, len(areas))
	for _, item := range areas {
		children[item.ParentID] = append(children[item.ParentID], item)
	}
	var build func(parentID string) []areaNode
	build = func(parentID string) []areaNode {
		items := children[parentID]
		out := make([]areaNode, 0, len(items))
		for _, item := range items {
			out = append(out, areaNode{
				ID:       item.ID,
				Name:     item.Name,
				ParentID: item.ParentID,
				IsRoot:   item.IsRoot,
				Sort:     item.Sort,
				Children: build(item.ID),
			})
		}
		return out
	}
	s.ok(c, gin.H{"items": build(""), "flat": areas})
}

func (s *Server) createArea(c *gin.Context) {
	var in areaUpsertRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		s.fail(c, http.StatusBadRequest, "name is required")
		return
	}
	parentID := strings.TrimSpace(in.ParentID)
	if parentID == "" {
		parentID = model.RootAreaID
	}

	var parent model.Area
	if err := s.db.Where("id = ?", parentID).First(&parent).Error; err != nil {
		s.fail(c, http.StatusBadRequest, "parent area not found")
		return
	}

	area := model.Area{ID: uuid.NewString(), Name: in.Name, ParentID: parentID, Sort: in.Sort, IsRoot: false}
	if err := s.db.Create(&area).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "create area failed")
		return
	}
	s.ok(c, area)
}

func (s *Server) updateArea(c *gin.Context) {
	id := c.Param("id")
	var area model.Area
	if err := s.db.Where("id = ?", id).First(&area).Error; err != nil {
		s.fail(c, http.StatusNotFound, "area not found")
		return
	}

	var in areaUpsertRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		s.fail(c, http.StatusBadRequest, "name is required")
		return
	}
	area.Name = name
	area.Sort = in.Sort
	parentID := strings.TrimSpace(in.ParentID)
	if !area.IsRoot && parentID != "" && parentID != area.ID {
		area.ParentID = parentID
	}
	if err := s.db.Save(&area).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "update area failed")
		return
	}
	s.ok(c, area)
}

func (s *Server) deleteArea(c *gin.Context) {
	id := c.Param("id")
	var area model.Area
	if err := s.db.Where("id = ?", id).First(&area).Error; err != nil {
		s.fail(c, http.StatusNotFound, "area not found")
		return
	}
	if area.IsRoot || area.ID == model.RootAreaID {
		s.fail(c, http.StatusBadRequest, "root area cannot be deleted")
		return
	}

	var deviceCount int64
	if err := s.db.Model(&model.Device{}).Where("area_id = ?", id).Count(&deviceCount).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "query devices failed")
		return
	}
	if deviceCount > 0 {
		s.fail(c, http.StatusBadRequest, "area contains devices and cannot be deleted")
		return
	}

	var childCount int64
	if err := s.db.Model(&model.Area{}).Where("parent_id = ?", id).Count(&childCount).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "query child areas failed")
		return
	}
	if childCount > 0 {
		s.fail(c, http.StatusBadRequest, "area contains child areas and cannot be deleted")
		return
	}

	if err := s.db.Delete(&model.Area{}, "id = ?", id).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "delete area failed")
		return
	}
	s.ok(c, gin.H{"deleted": id})
}
