package urihandler

import (
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseURI(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected *URI
		err      string
	}{
		{
			name:  "Valid TCP URI",
			input: "tcp://localhost:9999",
			expected: &URI{
				Scheme: "tcp",
				Host:   "localhost",
				Port:   9999,
				Path:   "",
				Exists: true,
				string: "tcp://localhost:9999",
			},
			err: "",
		},
		{
			name:  "Valid UDP URI",
			input: "udp://localhost:8888",
			expected: &URI{
				Scheme: "udp",
				Host:   "localhost",
				Port:   8888,
				Path:   "",
				Exists: true,
				string: "udp://localhost:8888",
			},
			err: "",
		},
		{
			name:  "Valid File URI",
			input: "file:///tmp/validfilepath",
			expected: &URI{
				Scheme: "file",
				Host:   "",
				Port:   0,
				Path:   "/tmp/validfilepath",
				Exists: true,
				string: "file:///tmp/validfilepath",
			},
			err: "",
		},
		{
			name:  "Valid Pipe URI",
			input: "pipe:///tmp/mypipe",
			expected: &URI{
				Scheme: "pipe",
				Host:   "",
				Port:   0,
				Path:   "/tmp/mypipe",
				Exists: true,
				string: "pipe:///tmp/mypipe",
			},
			err: "",
		},
		{
			name:  "Valid Unix Socket URI",
			input: "unix:///tmp/mysocket",
			expected: &URI{
				Scheme: "unix",
				Host:   "",
				Port:   0,
				Path:   "/tmp/mysocket",
				Exists: true,
				string: "unix:///tmp/mysocket",
			},
			err: "",
		},
		{
			name:     "TCP URI Missing Port",
			input:    "tcp://localhost",
			expected: nil,
			err:      "port must be specified for tcp scheme",
		},
		{
			name:     "UDP URI Missing Port",
			input:    "udp://localhost",
			expected: nil,
			err:      "port must be specified for udp scheme",
		},
		{
			name:     "Unknown Scheme",
			input:    "notascheme://localhost",
			expected: nil,
			err:      "invalid scheme: notascheme",
		},
		{
			name:  "Implicit File Path",
			input: "/tmp/validfilepath",
			expected: &URI{
				Scheme: "file",
				Host:   "",
				Port:   0,
				Path:   "/tmp/validfilepath",
				Exists: true,
				string: "/tmp/validfilepath",
			},
			err: "",
		},
		{
			name:  "Non-Existent Implicit File Path",
			input: "/tmp/nonexistentfile",
			expected: &URI{
				Scheme: "file",
				Host:   "",
				Port:   0,
				Path:   "/tmp/nonexistentfile",
				Exists: false,
				string: "/tmp/nonexistentfile",
			},
			err: "",
		},
	}

	// Create dummy files, pipes, and sockets for testing
	createDummyFile("/tmp/validfilepath", t)
	defer os.Remove("/tmp/validfilepath")

	createDummyPipe("/tmp/mypipe", t)
	defer os.Remove("/tmp/mypipe")

	listener := createDummySocket("/tmp/mysocket", t)
	defer listener.Close()
	defer os.Remove("/tmp/mysocket")

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := ParseURI(test.input)
			if test.err != "" {
				if err == nil || err.Error() != test.err {
					t.Errorf("expected error %v, got %v", test.err, err)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if result != nil {
					result.Path = normalizePath(result.Path)
					test.expected.Path = normalizePath(test.expected.Path)
				}
				// Test using the String() method
				assert.Equal(t, test.expected.String(), result.String(), "expected string %v, got %v", test.expected.string, result.String())
			}
		})
	}
}

func TestURIStructStringMethod(t *testing.T) {
	tests := []struct {
		name     string
		input    *URI
		expected string
	}{
		{
			name: "TCP URI",
			input: &URI{
				Scheme: "tcp",
				Host:   "localhost",
				Port:   9999,
				Path:   "",
				Exists: true,
				string: "tcp://localhost:9999",
			},
			expected: "tcp://localhost:9999",
		},
		{
			name: "UDP URI",
			input: &URI{
				Scheme: "udp",
				Host:   "localhost",
				Port:   8888,
				Path:   "",
				Exists: true,
				string: "udp://localhost:8888",
			},
			expected: "udp://localhost:8888",
		},
		{
			name: "File URI",
			input: &URI{
				Scheme: "file",
				Host:   "",
				Port:   0,
				Path:   "/tmp/validfilepath",
				Exists: true,
				string: "file:///tmp/validfilepath",
			},
			expected: "file:///tmp/validfilepath",
		},
		{
			name: "Pipe URI",
			input: &URI{
				Scheme: "pipe",
				Host:   "",
				Port:   0,
				Path:   "/tmp/mypipe",
				Exists: true,
				string: "pipe:///tmp/mypipe",
			},
			expected: "pipe:///tmp/mypipe",
		},
		{
			name: "Unix Socket URI",
			input: &URI{
				Scheme: "unix",
				Host:   "",
				Port:   0,
				Path:   "/tmp/mysocket",
				Exists: true,
				string: "unix:///tmp/mysocket",
			},
			expected: "unix:///tmp/mysocket",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if result := test.input.String(); result != test.expected {
				t.Errorf("expected %v, got %v", test.expected, result)
			}
		})
	}
}

// createDummyFile is a helper function to create a dummy file for testing
func createDummyFile(path string, t *testing.T) {
	_, err := os.Create(path)
	if err != nil {
		t.Fatalf("Failed to create dummy file: %v", err)
	}
}

// createDummyPipe is a helper function to create a dummy pipe for testing
func createDummyPipe(path string, t *testing.T) {
	err := syscall.Mkfifo(path, 0666)
	if err != nil {
		t.Fatalf("Failed to create dummy pipe: %v", err)
	}
}

// createDummySocket is a helper function to create a dummy Unix socket for testing
func createDummySocket(path string, t *testing.T) *net.UnixListener {
	addr := net.UnixAddr{Name: path, Net: "unix"}
	listener, err := net.ListenUnix("unix", &addr)
	if err != nil {
		t.Fatalf("Failed to create dummy Unix socket: %v", err)
	}
	return listener
}

// normalizePath is a helper function to convert paths to a consistent format for comparison
func normalizePath(path string) string {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return absPath
}
