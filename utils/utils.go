package utils

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/MagalixTechnologies/log-go"
	"github.com/MagalixTechnologies/uuid-go"
	"github.com/ryanuber/go-glob"
)

var stderr *log.Logger

func SetLogger(logger *log.Logger) {
	stderr = logger
}

func ExpandEnv(
	args map[string]interface{},
	flag string,
	allowEmpty bool,
) string {
	defer func() {
		tears := recover()
		if tears != nil {
			panic(fmt.Sprintf("invalid docopt for %s", flag))
		}
	}()

	key, ok := args[flag].(string)
	if !ok || len(key) == 0 {
		if allowEmpty {
			return ""
		}

		stderr.Fatalf(nil, "no flag value %s specified", flag)
		os.Exit(1)
	}

	if key[0] != '$' {
		return key
	}

	value := os.Getenv(key[1:])
	if len(value) == 0 {
		stderr.Fatalf(
			nil,
			"no such environment variable: %s (specified as %s flag)",
			key, flag,
		)
		os.Exit(1)
	}

	return value
}

func ExpandEnvUUID(
	args map[string]interface{},
	flag string,
) uuid.UUID {
	defer func() {
		tears := recover()
		if tears != nil {
			panic(fmt.Sprintf("invalid docopt for %s", flag))
		}
	}()

	key, ok := args[flag].(string)
	if !ok || len(key) == 0 {
		stderr.Fatalf(nil, "no flag value %s specified", flag)
		os.Exit(1)
	}

	if key[0] != '$' {
		id, err := uuid.FromString(key)
		if err != nil {
			stderr.Fatalf(err, "invalid UUID specified as %s flag", flag)
			os.Exit(1)
		}

		return id
	}

	value := os.Getenv(key[1:])
	if len(value) == 0 {
		stderr.Fatalf(
			nil,
			"no such environment variable: %s (specified as %s flag)",
			key, flag,
		)
		os.Exit(1)
	}

	id, err := uuid.FromString(value)
	if err != nil {
		stderr.Fatalf(
			err,
			"invalid UUID specified as %s environment variable (obtained using %s flag)",
			key,
			flag,
		)
		os.Exit(1)
	}

	return id
}

func MustParseDuration(args map[string]interface{}, flag string) time.Duration {
	defer func() {
		tears := recover()
		if tears != nil {
			panic(fmt.Sprintf("invalid docopt for %s", flag))
		}
	}()

	duration, err := time.ParseDuration(args[flag].(string))
	if err != nil {
		stderr.Fatalf(err, "unable to parse %s value as duration", flag)
		os.Exit(1)
	}

	return duration
}

func MustParseInt(args map[string]interface{}, flag string) int {
	defer func() {
		tears := recover()
		if tears != nil {
			panic(fmt.Sprintf("invalid docopt for %s", flag))
		}
	}()

	number, err := strconv.Atoi(args[flag].(string))
	if err != nil {
		stderr.Fatalf(err, "unable to parse %s value as integer", flag)
		os.Exit(1)
	}

	return number
}

func GetSanitizedArgs() []string {
	sensitive := []string{"--client-secret"}

	args := []string{}

args:
	for i := 0; i < len(os.Args); i++ {
		arg := os.Args[i]
		for _, flag := range sensitive {
			if strings.HasPrefix(arg, flag) {
				var value string
				if strings.HasPrefix(arg, flag+"=") {
					value = strings.TrimPrefix(arg, flag+"=")
					// no need to hide value if it's name of env variable
					if value != "" && !strings.HasPrefix(value, "$") {
						arg = flag + "=<sensitive:" + fmt.Sprint(len(value)) + ">"
					}

					args = append(args, arg)
				} else {
					args = append(args, arg)
					if len(os.Args) > i+1 {
						value = os.Args[i+1]
						if value != "" && !strings.HasPrefix(value, "$") {
							value = "<sensitive:" + fmt.Sprint(len(value)) + ">"
						}
						args = append(args, value)
						i++
					}
				}

				continue args
			}
		}

		args = append(args, arg)
	}

	return args
}

func InSkipNamespace(skipNamespacePatterns []string, namespace string) bool {

	for _, pattern := range skipNamespacePatterns {
		matched := glob.Glob(pattern, namespace)
		if matched {
			return true
		}
	}

	return false
}

func Throttle(
	name string,
	interval time.Duration,
	tickLimit int32,
	fn func(args ...interface{})) func(args ...interface{},
) {
	getNextTick := func() time.Time {
		return time.Now().
			Truncate(time.Second).
			Truncate(interval).
			Add(interval)
	}

	nextTick := getNextTick()

	stderr.Info("{%s throttler} next tick at %s", name, nextTick.Format(time.RFC3339))

	var tickFires int32 = 0

	return func(args ...interface{}) {
		now := time.Now()
		if now.After(nextTick) || now.Equal(nextTick) {
			stderr.Info("{%s throttler} ticking", name)
			fn(args...)

			atomic.AddInt32(&tickFires, 1)
			if tickFires >= tickLimit {
				atomic.StoreInt32(&tickFires, 0)
				nextTick = getNextTick()
			}

			stderr.Info("{%s throttler} next tick at %s", name, nextTick.Format(time.RFC3339))
		} else {
			stderr.Info("{%s throttler} throttled", name)
		}
	}
}

// After returns pointer to time after specific duration
func After(d time.Duration) *time.Time {
	t := time.Now().Add(d)
	return &t
}

func TruncateString(str string, num int) string {
	truncated := str
	if len(str) > num {
		if num > 3 {
			num -= 3
		}
		truncated = str[0:num] + "..."
	}
	return truncated
}
