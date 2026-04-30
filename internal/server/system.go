package server

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"maas-box/internal/model"
)

type userUpsertRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Enabled  bool   `json:"enabled"`
}

type roleUpsertRequest struct {
	Name   string `json:"name"`
	Remark string `json:"remark"`
}

type menuUpsertRequest struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	MenuType string `json:"menu_type"`
	ViewPath string `json:"view_path"`
	Icon     string `json:"icon"`
	ParentID string `json:"parent_id"`
	Sort     int    `json:"sort"`
}

const (
	menuTypeDirectory = "directory"
	menuTypeMenu      = "menu"
)

type idsRequest struct {
	IDs []string `json:"ids"`
}

type settingUpdateRequest struct {
	LLMRole              string `json:"llm_role"`
	LLMOutputRequirement string `json:"llm_output_requirement"`
	AICallbackToken      string `json:"ai_callback_token"`
}

func (s *Server) registerSystemRoutes(r gin.IRouter) {
	g := r.Group("/system")
	g.GET("/users", s.listUsers)
	g.POST("/users", s.createUser)
	g.PUT("/users/:id", s.updateUser)
	g.DELETE("/users/:id", s.deleteUser)
	g.GET("/users/:id/roles", s.getUserRoles)
	g.PUT("/users/:id/roles", s.setUserRoles)

	g.GET("/roles", s.listRoles)
	g.POST("/roles", s.createRole)
	g.PUT("/roles/:id", s.updateRole)
	g.DELETE("/roles/:id", s.deleteRole)
	g.GET("/roles/:id/menus", s.getRoleMenus)
	g.PUT("/roles/:id/menus", s.setRoleMenus)

	g.GET("/menus", s.listMenus)
	g.POST("/menus", s.createMenu)
	g.PUT("/menus/:id", s.updateMenu)
	g.DELETE("/menus/:id", s.deleteMenu)

	g.GET("/metrics", s.systemMetrics)
}

func (s *Server) listUsers(c *gin.Context) {
	var users []model.User
	if err := s.db.Order("created_at desc").Find(&users).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "query users failed")
		return
	}
	s.ok(c, gin.H{"items": users, "total": len(users)})
}

func (s *Server) createUser(c *gin.Context) {
	var in userUpsertRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}
	in.Username = strings.TrimSpace(in.Username)
	if in.Username == "" || strings.TrimSpace(in.Password) == "" {
		s.fail(c, http.StatusBadRequest, "username and password are required")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		s.fail(c, http.StatusInternalServerError, "hash password failed")
		return
	}
	user := model.User{
		ID:           uuid.NewString(),
		Username:     in.Username,
		PasswordHash: string(hash),
		Enabled:      in.Enabled,
	}
	if err := s.db.Create(&user).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "create user failed")
		return
	}
	s.ok(c, user)
}

func (s *Server) updateUser(c *gin.Context) {
	id := c.Param("id")
	var user model.User
	if err := s.db.Where("id = ?", id).First(&user).Error; err != nil {
		s.fail(c, http.StatusNotFound, "user not found")
		return
	}
	var in userUpsertRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}
	if strings.TrimSpace(in.Username) != "" {
		user.Username = strings.TrimSpace(in.Username)
	}
	user.Enabled = in.Enabled
	if strings.TrimSpace(in.Password) != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
		if err != nil {
			s.fail(c, http.StatusInternalServerError, "hash password failed")
			return
		}
		user.PasswordHash = string(hash)
	}
	if err := s.db.Save(&user).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "update user failed")
		return
	}
	s.ok(c, user)
}

func (s *Server) deleteUser(c *gin.Context) {
	id := c.Param("id")
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&model.UserRole{}, "user_id = ?", id).Error; err != nil {
			return err
		}
		return tx.Delete(&model.User{}, "id = ?", id).Error
	}); err != nil {
		s.fail(c, http.StatusInternalServerError, "delete user failed")
		return
	}
	s.ok(c, gin.H{"deleted": id})
}

func (s *Server) setUserRoles(c *gin.Context) {
	userID := c.Param("id")
	var in idsRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}
	roleIDs := uniqueStrings(in.IDs)
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&model.UserRole{}, "user_id = ?", userID).Error; err != nil {
			return err
		}
		items := make([]model.UserRole, 0, len(roleIDs))
		for _, roleID := range roleIDs {
			items = append(items, model.UserRole{UserID: userID, RoleID: roleID})
		}
		if len(items) > 0 {
			return tx.Create(&items).Error
		}
		return nil
	}); err != nil {
		s.fail(c, http.StatusInternalServerError, "set user roles failed")
		return
	}
	s.ok(c, gin.H{"user_id": userID, "role_ids": roleIDs})
}

func (s *Server) getUserRoles(c *gin.Context) {
	userID := c.Param("id")
	var items []model.UserRole
	if err := s.db.Where("user_id = ?", userID).Find(&items).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "query user roles failed")
		return
	}
	roleIDs := make([]string, 0, len(items))
	for _, item := range items {
		roleIDs = append(roleIDs, item.RoleID)
	}
	s.ok(c, gin.H{"user_id": userID, "role_ids": roleIDs})
}

func (s *Server) listRoles(c *gin.Context) {
	var roles []model.Role
	if err := s.db.Order("created_at asc").Find(&roles).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "query roles failed")
		return
	}
	s.ok(c, gin.H{"items": roles, "total": len(roles)})
}

func (s *Server) createRole(c *gin.Context) {
	var in roleUpsertRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		s.fail(c, http.StatusBadRequest, "role name is required")
		return
	}
	role := model.Role{
		ID:     uuid.NewString(),
		Name:   strings.TrimSpace(in.Name),
		Remark: strings.TrimSpace(in.Remark),
	}
	if err := s.db.Create(&role).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "create role failed")
		return
	}
	s.ok(c, role)
}

func (s *Server) updateRole(c *gin.Context) {
	id := c.Param("id")
	var role model.Role
	if err := s.db.Where("id = ?", id).First(&role).Error; err != nil {
		s.fail(c, http.StatusNotFound, "role not found")
		return
	}
	var in roleUpsertRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		s.fail(c, http.StatusBadRequest, "role name is required")
		return
	}
	role.Name = strings.TrimSpace(in.Name)
	role.Remark = strings.TrimSpace(in.Remark)
	if err := s.db.Save(&role).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "update role failed")
		return
	}
	s.ok(c, role)
}

func (s *Server) deleteRole(c *gin.Context) {
	id := c.Param("id")
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&model.RoleMenu{}, "role_id = ?", id).Error; err != nil {
			return err
		}
		if err := tx.Delete(&model.UserRole{}, "role_id = ?", id).Error; err != nil {
			return err
		}
		return tx.Delete(&model.Role{}, "id = ?", id).Error
	}); err != nil {
		s.fail(c, http.StatusInternalServerError, "delete role failed")
		return
	}
	s.ok(c, gin.H{"deleted": id})
}

func (s *Server) setRoleMenus(c *gin.Context) {
	roleID := c.Param("id")
	var in idsRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}
	menuIDs := uniqueStrings(in.IDs)
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&model.RoleMenu{}, "role_id = ?", roleID).Error; err != nil {
			return err
		}
		items := make([]model.RoleMenu, 0, len(menuIDs))
		for _, menuID := range menuIDs {
			items = append(items, model.RoleMenu{RoleID: roleID, MenuID: menuID})
		}
		if len(items) > 0 {
			return tx.Create(&items).Error
		}
		return nil
	}); err != nil {
		s.fail(c, http.StatusInternalServerError, "set role menus failed")
		return
	}
	s.ok(c, gin.H{"role_id": roleID, "menu_ids": menuIDs})
}

func (s *Server) getRoleMenus(c *gin.Context) {
	roleID := c.Param("id")
	var items []model.RoleMenu
	if err := s.db.Where("role_id = ?", roleID).Find(&items).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "query role menus failed")
		return
	}
	menuIDs := make([]string, 0, len(items))
	for _, item := range items {
		menuIDs = append(menuIDs, item.MenuID)
	}
	s.ok(c, gin.H{"role_id": roleID, "menu_ids": menuIDs})
}

func (s *Server) listMenus(c *gin.Context) {
	var items []model.Menu
	if err := s.db.Order("sort asc, created_at asc").Find(&items).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "query menus failed")
		return
	}
	s.ok(c, gin.H{"items": items, "total": len(items)})
}

func normalizeMenuType(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case menuTypeDirectory:
		return menuTypeDirectory
	case menuTypeMenu:
		return menuTypeMenu
	default:
		return menuTypeMenu
	}
}

func (s *Server) validateMenuParent(parentID, selfID string) error {
	parentID = strings.TrimSpace(parentID)
	if parentID == "" {
		return nil
	}
	if selfID != "" && parentID == selfID {
		return fmt.Errorf("parent menu cannot be self")
	}
	var count int64
	if err := s.db.Model(&model.Menu{}).Where("id = ?", parentID).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("parent menu not found")
	}
	return nil
}

func (s *Server) createMenu(c *gin.Context) {
	var in menuUpsertRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}
	name := strings.TrimSpace(in.Name)
	path := strings.TrimSpace(in.Path)
	menuType := normalizeMenuType(in.MenuType)
	viewPath := strings.TrimSpace(in.ViewPath)
	icon := strings.TrimSpace(in.Icon)
	parentID := strings.TrimSpace(in.ParentID)

	if name == "" {
		s.fail(c, http.StatusBadRequest, "name is required")
		return
	}
	if menuType == menuTypeMenu && path == "" {
		s.fail(c, http.StatusBadRequest, "path is required for menu")
		return
	}
	if menuType == menuTypeMenu && viewPath == "" {
		s.fail(c, http.StatusBadRequest, "view_path is required for menu")
		return
	}
	if menuType == menuTypeDirectory {
		viewPath = ""
	}
	if err := s.validateMenuParent(parentID, ""); err != nil {
		s.fail(c, http.StatusBadRequest, err.Error())
		return
	}
	item := model.Menu{
		ID:       uuid.NewString(),
		Name:     name,
		Path:     path,
		MenuType: menuType,
		ViewPath: viewPath,
		Icon:     icon,
		ParentID: parentID,
		Sort:     in.Sort,
	}
	if err := s.db.Create(&item).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "create menu failed")
		return
	}
	s.ok(c, item)
}

func (s *Server) updateMenu(c *gin.Context) {
	id := c.Param("id")
	var item model.Menu
	if err := s.db.Where("id = ?", id).First(&item).Error; err != nil {
		s.fail(c, http.StatusNotFound, "menu not found")
		return
	}
	var in menuUpsertRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}
	name := strings.TrimSpace(in.Name)
	path := strings.TrimSpace(in.Path)
	menuType := normalizeMenuType(in.MenuType)
	viewPath := strings.TrimSpace(in.ViewPath)
	icon := strings.TrimSpace(in.Icon)
	parentID := strings.TrimSpace(in.ParentID)

	if name == "" {
		s.fail(c, http.StatusBadRequest, "name is required")
		return
	}
	if menuType == menuTypeMenu && path == "" {
		s.fail(c, http.StatusBadRequest, "path is required for menu")
		return
	}
	if menuType == menuTypeMenu && viewPath == "" {
		s.fail(c, http.StatusBadRequest, "view_path is required for menu")
		return
	}
	if menuType == menuTypeDirectory {
		viewPath = ""
	}
	if err := s.validateMenuParent(parentID, id); err != nil {
		s.fail(c, http.StatusBadRequest, err.Error())
		return
	}
	item.Name = name
	item.Path = path
	item.MenuType = menuType
	item.ViewPath = viewPath
	item.Icon = icon
	item.ParentID = parentID
	item.Sort = in.Sort
	if err := s.db.Save(&item).Error; err != nil {
		s.fail(c, http.StatusInternalServerError, "update menu failed")
		return
	}
	s.ok(c, item)
}

func (s *Server) deleteMenu(c *gin.Context) {
	id := c.Param("id")
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&model.RoleMenu{}, "menu_id = ?", id).Error; err != nil {
			return err
		}
		return tx.Delete(&model.Menu{}, "id = ?", id).Error
	}); err != nil {
		s.fail(c, http.StatusInternalServerError, "delete menu failed")
		return
	}
	s.ok(c, gin.H{"deleted": id})
}

func (s *Server) getSettings(c *gin.Context) {
	s.ok(c, gin.H{
		"llm_role":               s.getSetting("llm_role"),
		"llm_output_requirement": s.getSetting("llm_output_requirement"),
		"ai_callback_token":      strings.TrimSpace(s.cfg.Server.AI.CallbackToken),
	})
}

func (s *Server) updateSettings(c *gin.Context) {
	var in settingUpdateRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}
	if strings.TrimSpace(in.LLMRole) != "" {
		if err := s.upsertSetting("llm_role", strings.TrimSpace(in.LLMRole)); err != nil {
			s.fail(c, http.StatusInternalServerError, "update llm_role failed")
			return
		}
	}
	if strings.TrimSpace(in.LLMOutputRequirement) != "" {
		if err := s.upsertSetting("llm_output_requirement", strings.TrimSpace(in.LLMOutputRequirement)); err != nil {
			s.fail(c, http.StatusInternalServerError, "update llm_output_requirement failed")
			return
		}
	}
	if strings.TrimSpace(in.AICallbackToken) != "" {
		s.cfg.Server.AI.CallbackToken = strings.TrimSpace(in.AICallbackToken)
	}
	s.ok(c, gin.H{
		"llm_role":               s.getSetting("llm_role"),
		"llm_output_requirement": s.getSetting("llm_output_requirement"),
		"ai_callback_token":      s.cfg.Server.AI.CallbackToken,
	})
}

func (s *Server) systemMetrics(c *gin.Context) {
	s.ok(c, s.collectRuntimeMetrics(time.Now()))
}
