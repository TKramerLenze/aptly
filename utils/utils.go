// Package utils collects various services: simple operations, compression, etc.
package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// DirIsAccessible verifies that directory exists and is accessible
func DirIsAccessible(filename string) error {
	fileStat, err := os.Stat(filename)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("error checking directory '%s': %s", filename, err)
		}
	} else {
		if fileStat.Mode().Perm() == 0000 || unix.Access(filename, unix.W_OK) != nil {
			return fmt.Errorf("'%s' is inaccessible, check access rights", filename)
		}
	}
	return nil
}

// CreateTempPath creates a temporary file for a logical package filename.
func CreateTempPath(filename string) (string, error) {
	file, err := os.CreateTemp("", filepath.Base(filename))
	if err != nil {
		return "", err
	}

	if err := file.Close(); err != nil {
		_ = os.Remove(file.Name())
		return "", err
	}

	return file.Name(), nil
}

// SanitizePath removes leading '/', remove '..', '$' and '`'
func SanitizePath(path string) (result string) {
	result = strings.Replace(path, "..", "", -1)
	result = strings.Replace(result, "$", "", -1)
	result = strings.Replace(result, "`", "", -1)
	result = strings.TrimLeft(result, "/")
	return
}
