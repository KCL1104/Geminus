package config

import (
	"net"
	"strconv"
)

// Server is the API's listener.
type Server struct {
	Addr string // host:port; the host may be empty (all interfaces)
}

// LoadServer reads API_LISTEN_ADDR (default ":8080"; port 0 picks a free
// port, for tests).
func LoadServer(getenv func(string) string) (Server, error) {
	addr := getenv("API_LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	_, port, err := net.SplitHostPort(addr)
	n, perr := strconv.Atoi(port)
	if err != nil || perr != nil || n < 0 || n > 65535 {
		return Server{}, &Error{"API_LISTEN_ADDR", CodeInvalid, "must be host:port with a port from 0 to 65535"}
	}
	return Server{Addr: addr}, nil
}
