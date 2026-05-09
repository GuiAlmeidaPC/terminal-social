package config

import (
	"flag"
	"os"
	"strconv"
)

type Config struct {
	Listen         string
	HostKey        string
	DB             string
	LogLevel       string
	MaxSessions    int
	RateMsgPer10s  int
	DefaultRoom    string
}

func envStr(k, def string) string {
	if v, ok := os.LookupEnv(k); ok {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v, ok := os.LookupEnv(k); ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func Load() *Config {
	c := &Config{
		Listen:        envStr("TSOCIAL_LISTEN", "0.0.0.0:2222"),
		HostKey:       envStr("TSOCIAL_HOST_KEY", "/var/lib/tsocial/host_key"),
		DB:            envStr("TSOCIAL_DB", "/var/lib/tsocial/tsocial.db"),
		LogLevel:      envStr("TSOCIAL_LOG_LEVEL", "info"),
		MaxSessions:   envInt("TSOCIAL_MAX_SESSIONS", 500),
		RateMsgPer10s: envInt("TSOCIAL_RATE_MSG_PER_10S", 10),
		DefaultRoom:   envStr("TSOCIAL_DEFAULT_ROOM", "general"),
	}

	flag.StringVar(&c.Listen, "listen", c.Listen, "SSH bind address")
	flag.StringVar(&c.HostKey, "host_key", c.HostKey, "SSH host key path")
	flag.StringVar(&c.DB, "db", c.DB, "SQLite path")
	flag.StringVar(&c.LogLevel, "log_level", c.LogLevel, "log level")
	flag.IntVar(&c.MaxSessions, "max_sessions", c.MaxSessions, "max concurrent sessions")
	flag.IntVar(&c.RateMsgPer10s, "rate_msg_per_10s", c.RateMsgPer10s, "msg rate per 10s")
	flag.StringVar(&c.DefaultRoom, "default_room", c.DefaultRoom, "auto-join room")
	flag.Parse()
	return c
}
