package util

import (
	"sync"

	"github.com/mojocn/base64Captcha"
)

var (
	captchaStore  = base64Captcha.DefaultMemStore
	captchaDriver = base64Captcha.NewDriverDigit(60, 200, 4, 0.7, 80)
	captchaInst   *base64Captcha.Captcha
	captchaOnce   sync.Once
)

func initCaptcha() {
	captchaOnce.Do(func() {
		captchaInst = base64Captcha.NewCaptcha(captchaDriver, captchaStore)
	})
}

// GenerateCaptcha 生成图形验证码，返回 captcha_id 和 base64 图片
func GenerateCaptcha() (string, string, error) {
	initCaptcha()
	id, b64s, _, err := captchaInst.Generate()
	return id, b64s, err
}

// VerifyCaptcha 验证图形验证码，clear 为 true 时验证后清除
func VerifyCaptcha(id, answer string, clear bool) bool {
	initCaptcha()
	return captchaStore.Verify(id, answer, clear)
}
