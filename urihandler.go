package urihandler

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
)

type Mode string
type Role string
type Scheme string

func (s Scheme) String() string {
	return string(s)
}

const (
	Peer   Mode = "peer" // Unified mode for simplicity, denoting peer-to-peer behavior
	Server Mode = "server"
	Client Mode = "client"
	Reader Role = "reader"
	Writer Role = "writer"
)

const (
	Default        = "" // Default scheme for simplicity
	File    Scheme = "file"
	Pipe    Scheme = "pipe"
	Unix    Scheme = "unix"
	TCP     Scheme = "tcp"
	UDP     Scheme = "udp"
)

var Schemes = []Scheme{Default, File, Pipe, Unix, TCP, UDP}

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
	Scheme Scheme
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

	if err = ValidateScheme(Scheme(parsedURL.Scheme)); err != nil {
		fmt.Printf("Error validating scheme %s: %v\n", parsedURL.Scheme, err)
		return nil, err
	}

	if isFileLikeScheme(parsedURL.Scheme) {
		return parseFileLikeURI(parsedURL)
	}

	return parseNetworkURI(parsedURL)
}

func parseFileLikeURI(parsedURL *url.URL) (*URI, error) {
	scheme := Scheme(parsedURL.Scheme)
	if scheme == Default {
		scheme = File
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

func parseNetworkURI(parsedURL *url.URL) (*URI, error) {
	scheme := Scheme(parsedURL.Scheme)
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
		Scheme: scheme,
		Host:   host,
		Port:   port,
		Path:   parsedURL.Path,
		Exists: true, // Network URIs are assumed to exist for the purpose of URI parsing
	}, nil
}

// ValidateScheme checks if the given scheme is valid
func ValidateScheme(s Scheme) error {
	fmt.Printf("Validating scheme: %s\n", s)
	if slices.Contains(Schemes, s) {
		return nil
	}
	return errors.New("invalid scheme: " + string(s))
}

func isFileLikeScheme(scheme string) bool {
	return slices.Contains([]Scheme{Default, File, Pipe, Unix}, Scheme(scheme))
}

func validateFileType(scheme Scheme, mode os.FileMode) bool {
	switch scheme {
	case File:
		return mode.IsRegular()
	case Pipe:
		return mode&os.ModeNamedPipe != 0
	case Unix:
		return mode&os.ModeSocket != 0
	default:
		return false
	}
}
