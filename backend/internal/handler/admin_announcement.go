package handler

import (
	"time"

	"github.com/microcosm-cc/bluemonday"

	"github.com/gin-gonic/gin"

	"nexus-panel/internal/model"
	"nexus-panel/internal/repo"
	"nexus-panel/internal/response"
)

// AdminAnnouncementHandler 管理端公告处理器
type AdminAnnouncementHandler struct {
	announceRepo *repo.AnnouncementRepo
}

// NewAdminAnnouncementHandler 创建管理端公告处理器
func NewAdminAnnouncementHandler(ar *repo.AnnouncementRepo) *AdminAnnouncementHandler {
	return &AdminAnnouncementHandler{announceRepo: ar}
}

// adminAnnouncementRequest 公告创建/更新请求
// 兼容前端两种字段名: is_pinned / pinned
type adminAnnouncementRequest struct {
	Title    string `json:"title" binding:"required,max=255"`
	Content  string `json:"content"`
	IsPinned *bool  `json:"is_pinned"`
	Pinned   *bool  `json:"pinned"`
	// Publish: 1=立即发布(写入 published_at=now), 0=草稿(不写入)
	Publish int `json:"publish"`
}

// resolvePinned 从请求中解析 is_pinned(优先) / pinned(兼容)
func (r *adminAnnouncementRequest) resolvePinned() bool {
	if r.IsPinned != nil {
		return *r.IsPinned
	}
	if r.Pinned != nil {
		return *r.Pinned
	}
	return false
}

// AdminListAnnouncements [GET] /api/v1/admin/announcements
// 列表(分页)
func (h *AdminAnnouncementHandler) AdminListAnnouncements(c *gin.Context) {
	page, size := parsePage(c)
	list, total, err := h.announceRepo.List(page, size)
	if err != nil {
		response.Fail(c, response.CodeDBError)
		return
	}
	response.OK(c, gin.H{"list": list, "total": total})
}

// sanitizeHTML [S5 fix 2026-07-14] 清洗 HTML 防止 XSS
func sanitizeHTML(content string) string {
	p := bluemonday.UGCPolicy()
	return p.Sanitize(content)
}

// AdminCreateAnnouncement [POST] /api/v1/admin/announcements
// 创建公告(默认立即发布)
func (h *AdminAnnouncementHandler) AdminCreateAnnouncement(c *gin.Context) {
	var req adminAnnouncementRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}
	now := time.Now()
	a := &model.Announcement{
		Title:     req.Title,
		Content:   sanitizeHTML(req.Content), // [S5 fix] XSS 清洗
		IsPinned:  req.resolvePinned(),
		IsDeleted: false,
		CreatedAt: now,
	}
	if req.Publish != 0 {
		a.PublishedAt = &now
	}
	if err := h.announceRepo.Create(a); err != nil {
		response.Fail(c, response.CodeDBError)
		return
	}
	response.OK(c, a)
}

// AdminUpdateAnnouncement [PUT] /api/v1/admin/announcements/:id
// 更新公告(全量: title/content/is_pinned)
func (h *AdminAnnouncementHandler) AdminUpdateAnnouncement(c *gin.Context) {
	id := c.Param("id")
	var req adminAnnouncementRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}
	exist, err := h.announceRepo.GetByID(id)
	if err != nil {
		response.Fail(c, response.CodeNotFound)
		return
	}
	exist.Title = req.Title
	exist.Content = sanitizeHTML(req.Content) // [S5 fix] XSS 清洗
	exist.IsPinned = req.resolvePinned()
	if err := h.announceRepo.Update(exist); err != nil {
		response.Fail(c, response.CodeDBError)
		return
	}
	response.OK(c, exist)
}

// AdminDeleteAnnouncement [DELETE] /api/v1/admin/announcements/:id
// 软删除公告
func (h *AdminAnnouncementHandler) AdminDeleteAnnouncement(c *gin.Context) {
	id := c.Param("id")
	if err := h.announceRepo.SoftDelete(id); err != nil {
		response.Fail(c, response.CodeDBError)
		return
	}
	response.OKMsg(c, "已删除")
}

// pinRequest 置顶请求
type pinRequest struct {
	Pinned bool `json:"pinned"`
}

// AdminPinAnnouncement [PATCH] /api/v1/admin/announcements/:id/pin
// 置顶/取消置顶
func (h *AdminAnnouncementHandler) AdminPinAnnouncement(c *gin.Context) {
	id := c.Param("id")
	var req pinRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, response.CodeParamError)
		return
	}
	if err := h.announceRepo.SetPinned(id, req.Pinned); err != nil {
		response.Fail(c, response.CodeDBError)
		return
	}
	response.OK(c, gin.H{"id": id, "pinned": req.Pinned, "is_pinned": req.Pinned})
}
