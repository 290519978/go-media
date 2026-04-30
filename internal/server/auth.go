package server

import (
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"maas-box/internal/model"
)

type authClaims struct {
	UserID   string   `json:"user_id"`
	Username string   `json:"username"`
	Roles    []string `json:"roles"`
	jwt.RegisteredClaims
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) login(c *gin.Context) {
	var in loginRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		s.fail(c, http.StatusBadRequest, "invalid payload")
		return
	}
	in.Username = strings.TrimSpace(in.Username)
	if in.Username == "" || strings.TrimSpace(in.Password) == "" {
		s.fail(c, http.StatusBadRequest, "username and password are required")
		return
	}

	var user model.User
	if err := s.db.Where("username = ?", in.Username).First(&user).Error; err != nil {
		s.fail(c, http.StatusUnauthorized, "invalid username or password")
		return
	}
	if !user.Enabled {
		s.fail(c, http.StatusForbidden, "user is disabled")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(in.Password)); err != nil {
		s.fail(c, http.StatusUnauthorized, "invalid username or password")
		return
	}

	roles, err := s.getUserRoleNames(user.ID)
	if err != nil {
		s.fail(c, http.StatusInternalServerError, "load user roles failed")
		return
	}

	claims := authClaims{
		UserID:   user.ID,
		Username: user.Username,
		Roles:    roles,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Subject:   user.ID,
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, err := token.SignedString(s.jwtSecret)
	if err != nil {
		s.fail(c, http.StatusInternalServerError, "generate token failed")
		return
	}

	s.ok(c, gin.H{"token": tokenStr, "username": user.Username, "roles": roles})
}

func (s *Server) me(c *gin.Context) {
	claims := s.mustClaims(c)
	menus, _ := s.getMenusByRoles(claims.Roles)
	resp := gin.H{
		"user_id":          claims.UserID,
		"username":         claims.Username,
		"roles":            claims.Roles,
		"menus":            menus,
		"development_mode": strings.TrimSpace(s.cfg.Server.Development) == "hzwlzhg",
	}

	cleanupNotices := make([]map[string]any, 0, 2)
	now := time.Now()
	if notice, err := s.pendingCleanupSoftPressureNotice(now); err != nil {
		log.Printf("load pending cleanup soft pressure notice failed: %v", err)
	} else if notice != nil {
		cleanupNotices = append(cleanupNotices, s.cleanupSoftPressureNoticePayload(notice))
	}
	if notice, err := s.pendingCleanupRetentionNotice(now); err != nil {
		log.Printf("load pending cleanup retention notice failed: %v", err)
	} else if notice != nil {
		cleanupNotices = append(cleanupNotices, s.cleanupRetentionNoticePayload(notice))
	}
	if len(cleanupNotices) > 0 {
		resp["cleanup_notices"] = cleanupNotices
		resp["cleanup_notice"] = cleanupNotices[0]
	}
	if notice, err := s.pendingLLMTokenQuotaNotice(now); err != nil {
		log.Printf("load pending llm quota notice failed: %v", err)
	} else if notice != nil {
		resp["llm_quota_notice"] = s.llmTokenQuotaNoticePayload(notice)
	}
	s.ok(c, resp)
}

func (s *Server) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := strings.TrimSpace(c.GetHeader("Authorization"))
		if token == "" {
			token = strings.TrimSpace(c.Query("token"))
		}
		if token == "" {
			s.fail(c, http.StatusUnauthorized, "missing authorization")
			c.Abort()
			return
		}
		if strings.HasPrefix(strings.ToLower(token), "bearer ") {
			token = strings.TrimSpace(token[7:])
		}
		parsed, err := jwt.ParseWithClaims(token, &authClaims{}, func(token *jwt.Token) (any, error) {
			return s.jwtSecret, nil
		})
		if err != nil || parsed == nil || !parsed.Valid {
			s.fail(c, http.StatusUnauthorized, "invalid token")
			c.Abort()
			return
		}
		claims, ok := parsed.Claims.(*authClaims)
		if !ok {
			s.fail(c, http.StatusUnauthorized, "invalid token claims")
			c.Abort()
			return
		}
		c.Set("claims", claims)
		c.Next()
	}
}

func (s *Server) menuPermissionMiddleware(menuPath string) gin.HandlerFunc {
	menuPath = strings.TrimSpace(menuPath)
	return func(c *gin.Context) {
		if menuPath == "" {
			c.Next()
			return
		}
		claims := s.mustClaims(c)
		if claims == nil {
			s.fail(c, http.StatusForbidden, "permission denied")
			c.Abort()
			return
		}
		for _, role := range claims.Roles {
			if strings.EqualFold(strings.TrimSpace(role), "admin") {
				c.Next()
				return
			}
		}
		menus, err := s.getMenusByRoles(claims.Roles)
		if err != nil {
			s.fail(c, http.StatusInternalServerError, "load permission failed")
			c.Abort()
			return
		}
		for _, item := range menus {
			itemPath := strings.TrimSpace(item.Path)
			if itemPath == "" {
				continue
			}
			if strings.EqualFold(itemPath, menuPath) {
				c.Next()
				return
			}
			if strings.HasPrefix(strings.ToLower(itemPath), strings.ToLower(menuPath+"/")) {
				c.Next()
				return
			}
		}
		s.fail(c, http.StatusForbidden, "permission denied")
		c.Abort()
	}
}

func (s *Server) mustClaims(c *gin.Context) *authClaims {
	raw, exists := c.Get("claims")
	if !exists {
		return &authClaims{}
	}
	claims, ok := raw.(*authClaims)
	if !ok || claims == nil {
		return &authClaims{}
	}
	return claims
}

func (s *Server) getUserRoleNames(userID string) ([]string, error) {
	var roles []model.Role
	if err := s.db.
		Table("mb_roles").
		Joins("JOIN mb_user_roles ON mb_user_roles.role_id = mb_roles.id").
		Where("mb_user_roles.user_id = ?", userID).
		Find(&roles).Error; err != nil {
		return nil, err
	}
	out := make([]string, 0, len(roles))
	for _, role := range roles {
		out = append(out, role.Name)
	}
	return out, nil
}

func (s *Server) getMenusByRoles(roleNames []string) ([]model.Menu, error) {
	if len(roleNames) == 0 {
		return []model.Menu{}, nil
	}
	var menus []model.Menu
	if err := s.db.
		Table("mb_menus").
		Joins("JOIN mb_role_menus ON mb_role_menus.menu_id = mb_menus.id").
		Joins("JOIN mb_roles ON mb_roles.id = mb_role_menus.role_id").
		Where("mb_roles.name IN ?", roleNames).
		Order("mb_menus.sort ASC").
		Find(&menus).Error; err != nil {
		return nil, err
	}
	return menus, nil
}
