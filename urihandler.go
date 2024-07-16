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

type URI struct {
	Scheme string
	Host   string
	Port   int
	Path   string
	Exists bool
}

func ParseURI(uri string) (*URI, error) {
	parsedURL, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}

	if isFileLikeScheme(parsedURL.Scheme) {
		return parseFileLikeURI(parsedURL)
	}

	if !isSupportedScheme(parsedURL.Scheme) {
		return nil, fmt.Errorf("unknown or unsupported scheme: %s", parsedURL.Scheme)
	}

	return parseNetworkURI(parsedURL)
}

func isFileLikeScheme(scheme string) bool {
	return scheme == "" || scheme == "file" || scheme == "pipe" || scheme == "unix"
}

func parseFileLikeURI(parsedURL *url.URL) (*URI, error) {
	scheme := parsedURL.Scheme
	if scheme == "" {
		scheme = "file"
	}

	absolutePath, err := filepath.Abs(parsedURL.Path)
	if err != nil {
		return nil, err
	}

	fileInfo, err := os.Stat(absolutePath)
	exists := err == nil || !os.IsNotExist(err)

	if exists {
		exists = validateFileType(scheme, fileInfo.Mode())
	}

	return &URI{
		Scheme: scheme,
		Host:   "",
		Port:   0,
		Path:   absolutePath,
		Exists: exists,
	}, nil
}

func validateFileType(scheme string, mode os.FileMode) bool {
	switch scheme {
	case "file":
		return mode.IsRegular()
	case "pipe":
		return mode&os.ModeNamedPipe != 0
	case "unix":
		return mode&os.ModeSocket != 0
	default:
		return false
	}
}

func parseNetworkURI(parsedURL *url.URL) (*URI, error) {
	host := parsedURL.Hostname()
	portStr := parsedURL.Port()

	if portStr == "" {
		return nil, fmt.Errorf("port must be specified for %s scheme", parsedURL.Scheme)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("invalid port: %v", err)
	}

	return &URI{
		Scheme: parsedURL.Scheme,
		Host:   host,
		Port:   port,
		Path:   parsedURL.Path,
		Exists: true, // Network URIs are assumed to exist for the purpose of URI parsing
	}, nil
}

func isSupportedScheme(scheme string) bool {
	switch scheme {
	case "tcp", "udp":
		return true
	default:
		return false
	}
}
