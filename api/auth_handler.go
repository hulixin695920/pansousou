package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"pansou/config"
	"pansou/model"
	"pansou/repository"
	"pansou/service"
	"pansou/util"
)

// LoginRequest 登录请求结构
type LoginRequest struct {
	Username      string `json:"username" binding:"required"`
	Password      string `json:"password" binding:"required"`
	CaptchaID     string `json:"captcha_id"`     // 验证码ID（CAPTCHA_ENABLED 时必填）
	CaptchaAnswer string `json:"captcha_answer"` // 验证码答案（CAPTCHA_ENABLED 时必填）
}

// LoginResponse 登录响应结构
type LoginResponse struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
	Username  string `json:"username"`
}

// RegisterRequest 注册请求结构
type RegisterRequest struct {
	Username      string `json:"username" binding:"required"`
	Password      string `json:"password" binding:"required"`
	Email         string `json:"email"`
	CaptchaID     string `json:"captcha_id"`     // 验证码ID（CAPTCHA_ENABLED 时必填）
	CaptchaAnswer string `json:"captcha_answer"` // 验证码答案（CAPTCHA_ENABLED 时必填）
}

// formatCaptchaImage 确保返回完整的 data URI，base64Captcha 新版本可能已包含前缀，避免重复
func formatCaptchaImage(b64s string) string {
	if len(b64s) > 10 && b64s[:10] == "data:image" {
		return b64s
	}
	return "data:image/png;base64," + b64s
}

// CaptchaHandler 获取图形验证码
func CaptchaHandler(c *gin.Context) {
	id, b64s, err := util.GenerateCaptcha()
	if err != nil {
		c.JSON(http.StatusOK, model.NewErrorResponse(500, "生成验证码失败"))
		return
	}
	c.JSON(http.StatusOK, model.NewSuccessResponse(gin.H{
		"captcha_id":   id,
		"captcha_image": formatCaptchaImage(b64s),
	}))
}

// LoginHandler 处理用户登录
func LoginHandler(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, model.NewErrorResponse(400, "参数错误：用户名和密码不能为空"))
		return
	}

	// 验证码校验（启用时）
	if config.AppConfig.CaptchaEnabled {
		if req.CaptchaID == "" || req.CaptchaAnswer == "" {
			c.JSON(http.StatusOK, model.NewErrorResponse(400, "请填写验证码"))
			return
		}
		if !util.VerifyCaptcha(req.CaptchaID, req.CaptchaAnswer, true) {
			c.JSON(http.StatusOK, model.NewErrorResponse(400, "验证码错误"))
			return
		}
	}

	// 验证认证系统是否启用
	if !config.AppConfig.AuthEnabled {
		c.JSON(http.StatusOK, model.NewErrorResponse(403, "认证功能未启用"))
		return
	}

	// 数据库模式：从MySQL验证
	if config.AppConfig.DBEnabled {
		user, err := service.ValidateUserPassword(req.Username, req.Password)
		if err != nil {
			if err == repository.ErrUserNotFound {
				c.JSON(http.StatusOK, model.NewErrorResponse(401, "用户名或密码错误"))
				return
			}
			c.JSON(http.StatusOK, model.NewErrorResponse(500, "登录验证失败"))
			return
		}

		token, err := util.GenerateToken(
			user.Username,
			config.AppConfig.AuthJWTSecret,
			config.AppConfig.AuthTokenExpiry,
		)
		if err != nil {
			c.JSON(http.StatusOK, model.NewErrorResponse(500, "生成令牌失败"))
			return
		}

		expiresAt := time.Now().Add(config.AppConfig.AuthTokenExpiry).Unix()
		c.JSON(http.StatusOK, model.NewSuccessResponse(LoginResponse{
			Token:     token,
			ExpiresAt: expiresAt,
			Username:  user.Username,
		}))
		return
	}

	// 环境变量模式：从AUTH_USERS验证（兼容旧版）
	if len(config.AppConfig.AuthUsers) == 0 {
		c.JSON(http.StatusOK, model.NewErrorResponse(500, "认证系统未正确配置，请配置DB或AUTH_USERS"))
		return
	}

	storedPassword, exists := config.AppConfig.AuthUsers[req.Username]
	if !exists || storedPassword != req.Password {
		c.JSON(http.StatusOK, model.NewErrorResponse(401, "用户名或密码错误"))
		return
	}

	token, err := util.GenerateToken(
		req.Username,
		config.AppConfig.AuthJWTSecret,
		config.AppConfig.AuthTokenExpiry,
	)
	if err != nil {
		c.JSON(http.StatusOK, model.NewErrorResponse(500, "生成令牌失败"))
		return
	}

	expiresAt := time.Now().Add(config.AppConfig.AuthTokenExpiry).Unix()
	c.JSON(http.StatusOK, model.NewSuccessResponse(LoginResponse{
		Token:     token,
		ExpiresAt: expiresAt,
		Username:  req.Username,
	}))
}

// RegisterHandler 处理用户注册（仅数据库模式可用）
func RegisterHandler(c *gin.Context) {
	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, model.NewErrorResponse(400, "参数错误：用户名和密码不能为空"))
		return
	}

	if !config.AppConfig.DBEnabled {
		c.JSON(http.StatusOK, model.NewErrorResponse(403, "注册功能需要启用数据库"))
		return
	}

	// 验证码校验（启用时）
	if config.AppConfig.CaptchaEnabled {
		if req.CaptchaID == "" || req.CaptchaAnswer == "" {
			c.JSON(http.StatusOK, model.NewErrorResponse(400, "请填写验证码"))
			return
		}
		if !util.VerifyCaptcha(req.CaptchaID, req.CaptchaAnswer, true) {
			c.JSON(http.StatusOK, model.NewErrorResponse(400, "验证码错误"))
			return
		}
	}

	user, err := service.RegisterUser(req.Username, req.Password, req.Email)
	if err != nil {
		switch err {
		case service.ErrInvalidUsername:
			c.JSON(http.StatusOK, model.NewErrorResponse(400, "用户名格式无效，需3-32位字母数字下划线"))
		case service.ErrInvalidPassword:
			c.JSON(http.StatusOK, model.NewErrorResponse(400, "密码长度需至少6位"))
		case service.ErrUserExists:
			c.JSON(http.StatusOK, model.NewErrorResponse(409, "用户名已存在"))
		default:
			c.JSON(http.StatusOK, model.NewErrorResponse(500, "注册失败"))
		}
		return
	}

	c.JSON(http.StatusOK, model.NewSuccessResponse(gin.H{
		"message":  "注册成功",
		"username": user.Username,
		"id":       user.ID,
	}))
}

// VerifyHandler 验证token有效性
func VerifyHandler(c *gin.Context) {
	// 如果未启用认证，直接返回有效
	if !config.AppConfig.AuthEnabled {
		c.JSON(http.StatusOK, model.NewSuccessResponse(gin.H{
			"valid":    true,
			"message":  "认证功能未启用",
			"username": "",
		}))
		return
	}

	// 如果能到达这里，说明中间件已经验证通过
	username, exists := c.Get("username")
	if !exists {
		c.JSON(http.StatusOK, model.NewErrorResponse(401, "未授权"))
		return
	}

	c.JSON(http.StatusOK, model.NewSuccessResponse(gin.H{
		"valid":    true,
		"username": username,
	}))
}

// ProfileHandler 获取当前用户资料（需认证）
func ProfileHandler(c *gin.Context) {
	if !config.AppConfig.AuthEnabled {
		c.JSON(http.StatusOK, model.NewErrorResponse(403, "认证功能未启用"))
		return
	}

	username, exists := c.Get("username")
	if !exists {
		c.JSON(http.StatusOK, model.NewErrorResponse(401, "未授权"))
		return
	}

	usernameStr, ok := username.(string)
	if !ok {
		c.JSON(http.StatusOK, model.NewErrorResponse(500, "用户信息异常"))
		return
	}

	// 数据库模式：返回完整资料
	if config.AppConfig.DBEnabled {
		profile, err := service.GetUserProfile(usernameStr)
		if err != nil {
			if err == repository.ErrUserNotFound {
				c.JSON(http.StatusOK, model.NewErrorResponse(404, "用户不存在"))
				return
			}
			c.JSON(http.StatusOK, model.NewErrorResponse(500, "获取用户资料失败"))
			return
		}
		c.JSON(http.StatusOK, model.NewSuccessResponse(profile))
		return
	}

	// 环境变量模式：仅返回用户名
	c.JSON(http.StatusOK, model.NewSuccessResponse(gin.H{
		"username": usernameStr,
	}))
}

// LogoutHandler 退出登录（客户端删除token即可）
func LogoutHandler(c *gin.Context) {
	// JWT是无状态的，服务端不需要处理注销
	// 客户端删除存储的token即可
	c.JSON(http.StatusOK, model.NewSuccessResponse(gin.H{"message": "退出成功"}))
}
