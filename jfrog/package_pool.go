package jfrog

import (
	"fmt"
	"strings"

	"github.com/aptly-dev/aptly/aptly"
	"github.com/aptly-dev/aptly/utils"
	"github.com/jfrog/jfrog-client-go/artifactory"
)

// PackagePool is a JFrog-backed package pool implementation.
// Task 2 only wires constructor/config selection; method implementations land in Task 3.
type PackagePool struct {
	aptly.PackagePool

	manager    artifactory.ArtifactoryServicesManager
	repository string
	prefix     string
}

// NewPackagePool validates configuration and builds a JFrog client manager without network I/O.
func NewPackagePool(root utils.JFrogPoolStorage) (*PackagePool, error) {
	if strings.TrimSpace(root.URL) == "" {
		return nil, fmt.Errorf("jfrog package pool url is required")
	}
	if strings.TrimSpace(root.Repository) == "" {
		return nil, fmt.Errorf("jfrog package pool repository is required")
	}

	serviceConfig, err := createPublishedStorageConfig(
		root.URL,
		root.User,
		root.Password,
		root.APIKey,
		root.AccessToken,
	)
	if err != nil {
		return nil, fmt.Errorf("error building jfrog package pool client config: %w", err)
	}

	manager, err := artifactory.New(serviceConfig)
	if err != nil {
		return nil, fmt.Errorf("error creating jfrog package pool manager: %w", err)
	}

	return &PackagePool{
		manager:    manager,
		repository: root.Repository,
		prefix:     root.Prefix,
	}, nil
}
