package urihandler

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseURI(t *testing.T) {
	tests := []struct {
		input    string
		expected *URI
		err      string
	}{
		{
			input: "tcp://localhost:9999",
			expected: &URI{
				Scheme: "tcp",
				Host:   "localhost",
				Port:   9999,
				Path:   "",
			},
			err: "",
		},
		{
			input: "udp://localhost:8888",
			expected: &URI{
				Scheme: "udp",
				Host:   "localhost",
				Port:   8888,
				Path:   "",
			},
			err: "",
		},
		{
			input: "file:///tmp/testfile",
			expected: &URI{
				Scheme: "file",
				Host:   "",
				Port:   0,
				Path:   "/tmp/testfile",
			},
			err: "",
		},
		{
			input: "pipe:///tmp/mypipe",
			expected: &URI{
				Scheme: "pipe",
				Host:   "",
				Port:   0,
				Path:   "/tmp/mypipe",
			},
			err: "",
		},
		{
			input: "unix:///tmp/mysocket",
			expected: &URI{
				Scheme: "unix",
				Host:   "",
				Port:   0,
				Path:   "/tmp/mysocket",
			},
			err: "",
		},
		{
			input:    "tcp://localhost",
			expected: nil,
			err:      "port must be specified for tcp scheme",
		},
		{
			input:    "udp://localhost",
			expected: nil,
			err:      "port must be specified for udp scheme",
		},
		{
			input:    "invalid://localhost",
			expected: nil,
			err:      "unknown scheme: invalid",
		},
		{
			input: "/tmp/validfilepath",
			expected: &URI{
				Scheme: "file",
				Host:   "",
				Port:   0,
				Path:   "/tmp/validfilepath",
			},
			err: "",
		},
		{
			input:    "nonexistentfile",
			expected: nil,
			err:      "invalid URI or file path: nonexistentfile",
		},
	}

	// Create a dummy file for testing
	_, err := os.Create("/tmp/validfilepath")
	if err != nil {
		t.Fatalf("Failed to create dummy file: %v", err)
	}
	defer os.Remove("/tmp/validfilepath")

	for _, test := range tests {
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
			if !reflect.DeepEqual(result, test.expected) {
				t.Errorf("expected %v, got %v", test.expected, result)
			}
		}
	}
}

// normalizePath is a helper function to convert paths to a consistent format for comparison
func normalizePath(path string) string {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return absPath
}
