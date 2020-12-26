package utils

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/MagalixTechnologies/core/logger"
	"github.com/MagalixTechnologies/uuid-go"
)

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

		logger.Fatalw("no flag value specified", "flag", flag)
		os.Exit(1)
	}

	if key[0] != '$' {
		return key
	}

	value := os.Getenv(key[1:])
	if len(value) == 0 {
		logger.Fatalf(
			"no such environment variable", "no such environment variable: %s (specified as %s flag)",
			key,
			flag,
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
		logger.Fatalw("no flag value specified", "flag", flag)

		os.Exit(1)
	}

	if key[0] != '$' {
		id, err := uuid.FromString(key)
		if err != nil {
			logger.Fatalf("invalid UUID specified as %s flag", flag)
			os.Exit(1)
		}

		return id
	}

	value := os.Getenv(key[1:])
	if len(value) == 0 {
		logger.Fatalf(
			"no such environment variable: %s (specified as %s flag)",
			key,
			flag,
		)
		os.Exit(1)
	}

	id, err := uuid.FromString(value)
	if err != nil {
		logger.Fatalw(
			"invalid UUID specified",
			"envar", key,
			"flag", flag,
			"error", err,
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
		logger.Fatalw("unable to parse value as duration", "flag", flag, "error", err)
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
		logger.Fatalw("unable to parse value as integer", "flag", flag, "error", err)

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

func Transcode(
	u interface{},
	v interface{},
) error {
	b, err := json.Marshal(u)
	if err != nil {
		return fmt.Errorf(
			"unable to marshal %T to json, error: %w",
			u,
			err,
		)
	}

	err = json.Unmarshal(b, v)
	if err != nil {
		return fmt.Errorf(
			"unable to unmarshal json into %T, error: %w",
			v,
			err,
		)
	}

	return nil
}
