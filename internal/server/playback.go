package server

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type playbackStreamStatusPayload struct {
	Active bool   `json:"active"`
	App    string `json:"app"`
	Stream string `json:"stream"`
}

func (s *Server) registerPlaybackRoutes(r gin.IRouter) {
	g := r.Group("/playback")
	g.GET("/stream-status", s.getPlaybackStreamStatus)
}

func (s *Server) getPlaybackStreamStatus(c *gin.Context) {
	app := strings.TrimSpace(c.Query("app"))
	stream := strings.TrimSpace(c.Query("stream"))
	if stream == "" {
		s.fail(c, http.StatusBadRequest, "stream 不能为空")
		return
	}

	active, err := s.isZLMStreamActive(app, stream)
	if err != nil {
		s.fail(c, http.StatusBadGateway, "查询流状态失败: "+err.Error())
		return
	}

	s.ok(c, playbackStreamStatusPayload{
		Active: active,
		App:    app,
		Stream: stream,
	})
}
