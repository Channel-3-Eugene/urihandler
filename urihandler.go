package urihandler

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
)

type Mode string
type Role string

const (
	Peer   Mode = "peer" // Unified mode for simplicity, denoting peer-to-peer behavior
	Server Mode = "server"
	Client Mode = "client"
	Reader Role = "reader"
	Writer Role = "writer"
)

type URIHandler interface {
	Open() error
	Close() error
	Status() interface{}
}

// Common status interface for all handlers
type Status interface {
	GetMode() Mode
	GetRole() Role
	GetAddress() string
}

// Possible IO handlers we may eventually support:
// - SRT
// - RTMP
// - DVB
// - ASI
// - SCTE-35

type URI struct {
	Scheme string
	Host   string
	Port   int
	Path   string
	Exists bool
}

func ParseURI(uri string) (*URI, error) {
	exists := true

	parsedURL, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}

	// If no scheme is provided, check if the input is a valid file path
	if parsedURL.Scheme == "" || parsedURL.Scheme == "file" {
		absolutePath, err := filepath.Abs(parsedURL.Path)
		if err != nil {
			exists = false
		}

		println(absolutePath)
		_, err = os.Stat(absolutePath)
		if err != nil {
			exists = false
		}

		return &URI{
			Scheme: "file",
			Host:   "",
			Port:   0,
			Path:   absolutePath,
			Exists: exists,
		}, nil
	}

	host := parsedURL.Hostname()
	portStr := parsedURL.Port()
	path := parsedURL.Path

	var port int
	if portStr != "" {
		port, err = strconv.Atoi(portStr)
		if err != nil {
			return nil, fmt.Errorf("invalid port: %v", err)
		}
	} else {
		switch parsedURL.Scheme {
		case "tcp", "udp":
			return nil, fmt.Errorf("port must be specified for %s scheme", parsedURL.Scheme)
		case "unix", "pipe":
			port = 0 // No port needed for these schemes
		case "http", "https":
			return nil, fmt.Errorf("%s not supported", parsedURL.Scheme)
		default:
			return nil, fmt.Errorf("unknown scheme: %s", parsedURL.Scheme)
		}
	}

	return &URI{
		Scheme: parsedURL.Scheme,
		Host:   host,
		Port:   port,
		Path:   path,
		Exists: exists,
	}, nil
}
