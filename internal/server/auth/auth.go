package auth

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/kingsunb/NovaVeil/internal/conf"
	"github.com/kingsunb/NovaVeil/internal/op"
)

func GenerateJWTToken(expiresSec int) (string, int, error) {
	secret, err := op.AuthJWTSecretGet()
	if err != nil {
		return "", 0, err
	}
	now := time.Now()
	maxAge := int((15 * time.Minute).Seconds())
	if expiresSec > 0 {
		maxAge = expiresSec
	} else if expiresSec == -1 {
		maxAge = int((24 * time.Hour).Seconds())
	}
	// 有效期上限 24 小时: expires 由客户端提交, 不设上限等于可自签长期凭据,
	// 被盗 cookie 的窗口与「记住我」对齐到一天。
	const maxAgeCeiling = 24 * 3600
	if maxAge > maxAgeCeiling {
		maxAge = maxAgeCeiling
	}
	claims := &jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now),
		Issuer:    conf.APP_NAME,
		Audience:  jwt.ClaimStrings{conf.APP_NAME},
		ExpiresAt: jwt.NewNumericDate(now.Add(time.Duration(maxAge) * time.Second)),
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		return "", 0, err
	}
	return token, maxAge, nil
}

func VerifyJWTToken(token string) bool {
	secret, err := op.AuthJWTSecretGet()
	if err != nil {
		return false
	}
	jwtToken, err := jwt.Parse(token, func(token *jwt.Token) (interface{}, error) {
		// 钉死签名算法: 仅接受 HS256, 拒绝 alg=none/RS256/HS384/HS512 等。
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(secret), nil
	},
		// 强制要求 exp 声明: 无过期的 token 可永久凭据, 仅能靠轮换密钥撤销。
		jwt.WithExpirationRequired(),
		// 校验 issuer/audience: 防止其他系统用同一密钥签发的 token 交叉使用。
		jwt.WithIssuer(conf.APP_NAME),
		jwt.WithAudience(conf.APP_NAME),
	)
	if err != nil || !jwtToken.Valid {
		return false
	}
	return true
}

func GenerateAPIKey() (string, error) {
	const keyChars = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	b := make([]byte, 48)
	maxI := big.NewInt(int64(len(keyChars)))
	for i := range b {
		n, err := rand.Int(rand.Reader, maxI)
		if err != nil {
			return "", err
		}
		b[i] = keyChars[n.Int64()]
	}
	return "sk-" + conf.APP_NAME + "-" + string(b), nil
}
