package urihandler

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
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
	Open(context.Context) error
	GetDataChannel() chan []byte
	GetEventsChannel() chan error
	Close() error
	Status() Status
}

// Common status interface for all handlers
type Status interface {
	GetMode() Mode
	GetRole() Role
	GetAddress() string
	IsOpen() bool
}

type URI struct {
	Scheme Scheme
	Host   string
	Port   int
	Path   string
	Exists bool
	string string
}

func (u *URI) String() string {
	return u.string
}

func ParseURI(uri string) (*URI, error) {
	parsedURL, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}

	if err = ValidateScheme(Scheme(parsedURL.Scheme)); err != nil {
		return nil, err
	}

	newURI, err := parseSpecificURI(parsedURL)
	if err != nil {
		return nil, err
	}

	newURI.string = parsedURL.String()
	return newURI, nil
}

func parseSpecificURI(parsedURL *url.URL) (*URI, error) {
	scheme := Scheme(parsedURL.Scheme)
	host := ""
	port := 0
	path := parsedURL.Path
	exists := true

	if scheme == Pipe {
		if path == "" {
			// If no path is provided, use stdin for input or stdout for output
			return &URI{
				Scheme: scheme,
				Path:   "", // Empty path indicates stdin or stdout
				Exists: true,
			}, nil
		} else {
			// Check if it's a named pipe (FIFO)
			fileInfo, err := os.Stat(path)
			if err != nil || fileInfo.Mode()&os.ModeNamedPipe == 0 {
				return nil, fmt.Errorf("invalid named pipe or path: %s", path)
			}
			return &URI{
				Scheme: scheme,
				Path:   path,
				Exists: true,
			}, nil
		}
	}

	// Continue handling other file-like schemes as before
	if isFileLikeScheme(parsedURL.Scheme) {
		if scheme == Default {
			scheme = File
		}

		absolutePath, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		path = absolutePath

		fileInfo, err := os.Stat(absolutePath)
		exists = err == nil || !os.IsNotExist(err)
		if exists {
			exists = validateFileType(scheme, fileInfo.Mode())
		}
	} else {
		host = parsedURL.Hostname()
		portStr := parsedURL.Port()

		if portStr == "" {
			return nil, fmt.Errorf("port must be specified for %s scheme", parsedURL.Scheme)
		}

		parsedPort, err := strconv.Atoi(portStr)
		if err != nil {
			return nil, fmt.Errorf("invalid port: %v", err)
		}
		port = parsedPort
	}

	return &URI{
		Scheme: scheme,
		Host:   host,
		Port:   port,
		Path:   path,
		Exists: exists,
	}, nil
}

// ValidateScheme checks if the given scheme is valid
func ValidateScheme(s Scheme) error {
	for _, validScheme := range Schemes {
		if validScheme == s {
			return nil
		}
	}
	return errors.New("invalid scheme: " + string(s))
}

func isFileLikeScheme(scheme string) bool {
	fileLikeSchemes := []Scheme{Default, File, Unix}
	for _, fileLikeScheme := range fileLikeSchemes {
		if Scheme(scheme) == fileLikeScheme {
			return true
		}
	}
	return false
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
