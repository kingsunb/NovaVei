package op

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/charmbracelet/log"
	"github.com/kingsunb/NovaVeil/internal/model"
)

const (
	secretCipherPrefix = "nvenc1:"
	secretsKeyFilename = "secrets.key"
	secretsKeyBytes    = 32
)

var (
	secretsKeyMu sync.RWMutex
	secretsKey   []byte
)

// InitSecretKey 从数据目录加载或生成 AES-256 密钥(文件权限 0600)。
// 未调用时 EncryptSecret/DecryptSecret 保持明文透传, 便于单测。
func InitSecretKey(dataDir string) error {
	if dataDir == "" {
		dataDir = "data"
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("创建密钥目录失败: %w", err)
	}
	path := filepath.Join(dataDir, secretsKeyFilename)
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("读取凭据加密密钥失败: %w", err)
		}
		key := make([]byte, secretsKeyBytes)
		if _, err := io.ReadFull(rand.Reader, key); err != nil {
			return fmt.Errorf("生成凭据加密密钥失败: %w", err)
		}
		if err := os.WriteFile(path, key, 0o600); err != nil {
			return fmt.Errorf("写入凭据加密密钥失败: %w", err)
		}
		_ = os.Chmod(path, 0o600)
		raw = key
		log.Infof("created secrets encryption key at %s", path)
	}
	if len(raw) != secretsKeyBytes {
		return fmt.Errorf("凭据加密密钥长度非法")
	}
	secretsKeyMu.Lock()
	secretsKey = raw
	secretsKeyMu.Unlock()
	return nil
}

func currentSecretsKey() []byte {
	secretsKeyMu.RLock()
	defer secretsKeyMu.RUnlock()
	return secretsKey
}

// EncryptSecret 加密凭据。密钥未初始化或明文为空时原样返回; 已是密文前缀则不重复加密。
func EncryptSecret(plain string) string {
	if plain == "" || strings.HasPrefix(plain, secretCipherPrefix) {
		return plain
	}
	key := currentSecretsKey()
	if len(key) == 0 {
		return plain
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		log.Errorf("secrets cipher: %v", err)
		return plain
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		log.Errorf("secrets gcm: %v", err)
		return plain
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		log.Errorf("secrets nonce: %v", err)
		return plain
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return secretCipherPrefix + base64.StdEncoding.EncodeToString(sealed)
}

// DecryptSecret 解密凭据。无前缀视为历史明文。
func DecryptSecret(value string) string {
	if value == "" || !strings.HasPrefix(value, secretCipherPrefix) {
		return value
	}
	key := currentSecretsKey()
	if len(key) == 0 {
		log.Errorf("encrypted secret present but encryption key is not loaded")
		return value
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, secretCipherPrefix))
	if err != nil {
		log.Errorf("decode encrypted secret: %v", err)
		return value
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		log.Errorf("secrets cipher: %v", err)
		return value
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		log.Errorf("secrets gcm: %v", err)
		return value
	}
	nonceSize := gcm.NonceSize()
	if len(raw) < nonceSize {
		log.Errorf("encrypted secret too short")
		return value
	}
	plain, err := gcm.Open(nil, raw[:nonceSize], raw[nonceSize:], nil)
	if err != nil {
		log.Errorf("decrypt secret: %v", err)
		return value
	}
	return string(plain)
}

func sealChannelSecrets(channel model.Channel) model.Channel {
	out := cacheableChannel(channel)
	out.Key = EncryptSecret(out.Key)
	if out.Keys != nil {
		for i := range out.Keys {
			out.Keys[i].Key = EncryptSecret(out.Keys[i].Key)
		}
	}
	return out
}

func revealChannelSecrets(channel *model.Channel) {
	if channel == nil {
		return
	}
	channel.Key = DecryptSecret(channel.Key)
	for i := range channel.Keys {
		channel.Keys[i].Key = DecryptSecret(channel.Keys[i].Key)
	}
}

func sealAPIKeySecret(key string) string {
	return EncryptSecret(key)
}

func revealAPIKeySecret(key string) string {
	return DecryptSecret(key)
}
