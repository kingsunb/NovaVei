package handlers

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/kingsunb/NovaVeil/internal/model"
	"github.com/kingsunb/NovaVeil/internal/op"
	"github.com/kingsunb/NovaVeil/internal/server/auth"
	"github.com/kingsunb/NovaVeil/internal/server/middleware"
	"github.com/kingsunb/NovaVeil/internal/server/resp"
	"github.com/kingsunb/NovaVeil/internal/server/router"
)

func init() {
	router.NewGroupRouter("/api/v1/user").
		Use(middleware.RequireJSON()).
		AddRoute(
			router.NewRoute("/login", http.MethodPost).
				Handle(login),
		)
	router.NewGroupRouter("/api/v1/user").
		Use(middleware.Auth()).
		Use(middleware.RequireJSON()).
		AddRoute(
			router.NewRoute("/logout", http.MethodPost).
				Handle(logout),
		).
		AddRoute(
			router.NewRoute("/change-password", http.MethodPost).
				Handle(changePassword),
		).
		AddRoute(
			router.NewRoute("/change-username", http.MethodPost).
				Handle(changeUsername),
		).
		AddRoute(
			router.NewRoute("/status", http.MethodGet).
				Handle(status),
		)
}

func login(c *gin.Context) {
	ip := c.ClientIP()
	if allowed, retryAfter := loginLimiter.check(ip); !allowed {
		rejectRateLimited(c, retryAfter)
		return
	}
	var user model.UserLogin
	if err := c.ShouldBindJSON(&user); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if err := op.UserVerify(user.Username, user.Password); err != nil {
		loginLimiter.recordFailure(ip)
		resp.Error(c, http.StatusUnauthorized, resp.ErrUnauthorized)
		return
	}
	loginLimiter.reset(ip)
	token, maxAge, err := auth.GenerateJWTToken(user.Expire)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, resp.ErrInternalServer)
		return
	}
	middleware.SetAuthCookie(c, token, maxAge)
	resp.Success(c, model.UserStatus{
		Username:           user.Username,
		MustChangePassword: op.UserGet().MustChangePassword,
	})
}

func logout(c *gin.Context) {
	middleware.ClearAuthCookie(c)
	resp.Success(c, nil)
}

func changePassword(c *gin.Context) {
	var user model.UserChangePassword
	if err := c.ShouldBindJSON(&user); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if err := op.UserChangePassword(user.OldPassword, user.NewPassword); err != nil {
		if errors.Is(err, op.ErrIncorrectOldPassword) {
			resp.Error(c, http.StatusBadRequest, "incorrect old password")
			return
		}
		if errors.Is(err, op.ErrPasswordValidation) {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
		if errors.Is(err, op.ErrInitialPasswordFileCleanupFailed) {
			// 改密已生效, 仅初始密码文件清理失败: 返回成功并附带告警,
			// 不让客户端误以为改密未发生。
			resp.Success(c, "password changed successfully, but failed to remove the initial password file")
			return
		}
		resp.Error(c, http.StatusInternalServerError, resp.ErrDatabase)
		return
	}
	resp.Success(c, "password changed successfully")
}

func changeUsername(c *gin.Context) {
	var user model.UserChangeUsername
	if err := c.ShouldBindJSON(&user); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	// 用户名是唯一登录凭据标识: 空串或超长会把账号改到无法正常登录, 写库前校验。
	username := strings.TrimSpace(user.NewUsername)
	if n := utf8.RuneCountInString(username); n < 2 || n > 64 {
		resp.Error(c, http.StatusBadRequest, "用户名需为 2-64 个字符")
		return
	}
	if err := op.UserChangeUsername(username, user.Password); err != nil {
		if errors.Is(err, op.ErrUsernameUnchanged) {
			resp.Error(c, http.StatusBadRequest, "新用户名与原用户名相同")
			return
		}
		if errors.Is(err, op.ErrIncorrectOldPassword) {
			resp.Error(c, http.StatusBadRequest, "incorrect password")
			return
		}
		resp.Error(c, http.StatusInternalServerError, resp.ErrInternalServer)
		return
	}
	resp.Success(c, "username changed successfully")
}

func status(c *gin.Context) {
	u := op.UserGet()
	resp.Success(c, model.UserStatus{
		Username:           u.Username,
		MustChangePassword: u.MustChangePassword,
	})
}
