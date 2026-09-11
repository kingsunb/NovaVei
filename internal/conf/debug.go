// debug.go exposes the NovaVeil debug-mode flag, controlled by the
// NOVAEIL_DEBUG environment variable.
package conf

import (
	"os"
	"strings"
)

// debugEnvKey is the environment variable name that toggles debug mode.
var debugEnvKey = strings.ToUpper(APP_NAME) + "_DEBUG"

// IsDebug reports whether debug mode is enabled via the environment.
func IsDebug() bool {
	return os.Getenv(debugEnvKey) == "true"
}
