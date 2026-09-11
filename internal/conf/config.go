package conf

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/spf13/viper"
)

type Server struct {
	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`
	// TrustedProxies 是可信反向代理的 CIDR/IP 列表(如 ["127.0.0.1/32", "10.0.0.0/8"])。
	// 为空时不信任任何代理头: ClientIP 不采信可伪造的 X-Forwarded-For,
	// 登录限速等按 IP 的防护不可被伪造头绕过(直连部署安全默认)。
	// 反代部署需显式配置, 仅来自这些地址的 XFF 才被采信, TLS 反代后 ClientIP 仍为真实客户端 IP。
	// 也可通过环境变量 NOVAVEI_SERVER_TRUSTED_PROXIES 设置(逗号分隔)。
	TrustedProxies []string `mapstructure:"trusted_proxies"`
}

type Log struct {
	Level string `mapstructure:"level"`
}

type Database struct {
	Type string `mapstructure:"type"`
	Path string `mapstructure:"path"`
}

type Security struct {
	// CookieSecure 控制 auth cookie 是否携带 Secure 属性(仅 HTTPS 下发送), 反代终止 TLS 时应开启
	CookieSecure bool `mapstructure:"cookie_secure"`
}

type Config struct {
	Server   Server   `mapstructure:"server"`
	Log      Log      `mapstructure:"log"`
	Database Database `mapstructure:"database"`
	Security Security `mapstructure:"security"`
}

var AppConfig Config

func Load(path string) error {
	if path != "" {
		viper.SetConfigFile(path)
	} else {
		viper.SetConfigName("config")
		viper.SetConfigType("json")
		viper.AddConfigPath("data")
	}

	viper.AutomaticEnv()
	viper.SetEnvPrefix(APP_NAME)
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	setDefaults()

	if err := viper.ReadInConfig(); err == nil {
		log.Infof("Using config file: %s", viper.ConfigFileUsed())
	} else {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			log.Infof("Config file not found, creating default config")
			if err := os.MkdirAll("data", 0o700); err != nil {
				log.Errorf("Failed to create data directory: %v", err)
			}
			if err := viper.SafeWriteConfigAs("data/config.json"); err != nil {
				log.Errorf("Failed to create default config: %v", err)
			} else {
				_ = os.Chmod("data/config.json", 0o600)
			}
		} else {
			return fmt.Errorf("error reading config file: %w", err)
		}
	}

	if err := viper.Unmarshal(&AppConfig); err != nil {
		return fmt.Errorf("unable to decode config into struct: %w", err)
	}
	applyEnvOverrides()
	return nil
}

// applyEnvOverrides 处理 viper 无法自动从环境变量解析为切片的配置项。
// NOVAVEI_SERVER_TRUSTED_PROXIES 以逗号分隔时覆盖配置文件中的 trusted_proxies。
func applyEnvOverrides() {
	envKey := strings.ToUpper(APP_NAME) + "_SERVER_TRUSTED_PROXIES"
	if raw := os.Getenv(envKey); raw != "" {
		parts := strings.Split(raw, ",")
		result := make([]string, 0, len(parts))
		for _, p := range parts {
			if t := strings.TrimSpace(p); t != "" {
				result = append(result, t)
			}
		}
		if len(result) > 0 {
			AppConfig.Server.TrustedProxies = result
		}
	}
}

func setDefaults() {
	viper.SetDefault("server.host", "0.0.0.0")
	viper.SetDefault("server.port", 8080)
	viper.SetDefault("database.type", "sqlite")
	viper.SetDefault("database.path", "data/data.db")
	viper.SetDefault("log.level", "info")
	viper.SetDefault("security.cookie_secure", false)
}
