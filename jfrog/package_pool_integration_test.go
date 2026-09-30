package jfrog

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aptly-dev/aptly/files"
	"github.com/aptly-dev/aptly/utils"
	. "gopkg.in/check.v1"
)

// integrationEnv captures JFrog credentials at package init time, before Test hooks run.
// This protects against t.Setenv in public_test.go overwriting real credentials.
type integrationEnv struct {
	URL         string
	Repository  string
	Username    string
	Password    string
	APIKey      string
	AccessToken string
}

// envSnapshot holds the captured environment, immutable after init().
var envSnapshot integrationEnv

func init() {
	envSnapshot = integrationEnv{
		URL:         strings.TrimSpace(os.Getenv("JFROG_URL")),
		Repository:  strings.TrimSpace(os.Getenv("JFROG_REPOSITORY")),
		Username:    strings.TrimSpace(os.Getenv("JFROG_USERNAME")),
		Password:    strings.TrimSpace(os.Getenv("JFROG_PASSWORD")),
		APIKey:      strings.TrimSpace(os.Getenv("JFROG_APIKEY")),
		AccessToken: strings.TrimSpace(os.Getenv("JFROG_ACCESSTOKEN")),
	}
}

// hasValidCredentials checks if snapshot contains at least one supported credential mode.
func (e integrationEnv) hasValidCredentials() bool {
	hasUserPass := e.Username != "" && e.Password != ""
	hasAPIKey := e.APIKey != ""
	hasAccessToken := e.AccessToken != ""
	return hasUserPass || hasAPIKey || hasAccessToken
}

type PackagePoolIntegrationSuite struct {
	pool      *PackagePool
	checksums *files.MockChecksumStorage
	artifacts []integrationArtifact
}

type integrationArtifact struct {
	name string
	path string
}

var _ = Suite(&PackagePoolIntegrationSuite{})

func (s *PackagePoolIntegrationSuite) SetUpSuite(c *C) {
	// Use snapshot captured at package init, before Test hook overwrites credentials.
	if envSnapshot.URL == "" || envSnapshot.Repository == "" || !envSnapshot.hasValidCredentials() {
		c.Skip("skipping JFrog integration tests: missing URL, repository, or supported credentials")
	}
}

func (s *PackagePoolIntegrationSuite) SetUpTest(c *C) {
	_, currentFile, _, ok := runtime.Caller(0)
	c.Assert(ok, Equals, true)

	prefix := "aptly-pool-integration-" + strconv.FormatInt(time.Now().UnixNano(), 10)

	// Use snapshot captured at package init to ensure Test hook hasn't overwritten credentials.
	pool, err := NewPackagePool(utils.JFrogPoolStorage{
		Repository:  envSnapshot.Repository,
		URL:         envSnapshot.URL,
		User:        envSnapshot.Username,
		Password:    envSnapshot.Password,
		APIKey:      envSnapshot.APIKey,
		AccessToken: envSnapshot.AccessToken,
		Prefix:      prefix,
	})
	c.Assert(err, IsNil)

	s.pool = pool
	s.checksums = files.NewMockChecksumStorage().(*files.MockChecksumStorage)
	s.artifacts = []integrationArtifact{
		{
			name: "deb",
			path: filepath.Join(filepath.Dir(currentFile), "../system/files/libboost-program-options-dev_1.49.0.1_i386.deb"),
		},
		{
			name: "udeb",
			path: filepath.Join(filepath.Dir(currentFile), "../system/udebs/dmraid-udeb_1.0.0.rc16-4.1_i386.udeb"),
		},
		{
			name: "source",
			path: filepath.Join(filepath.Dir(currentFile), "../system/files/pyspi_0.6.1-1.3.dsc"),
		},
	}

	for _, artifact := range s.artifacts {
		_, statErr := os.Stat(artifact.path)
		c.Assert(statErr, IsNil)
	}
}

func (s *PackagePoolIntegrationSuite) TearDownTest(c *C) {
	if s.pool == nil {
		return
	}

	list, err := s.pool.FilepathList(nil)
	if err != nil {
		c.Errorf("cleanup listing failed: %v", err)
		return
	}

	for _, relPath := range list {
		_, removeErr := s.pool.Remove(relPath)
		if removeErr != nil {
			c.Errorf("cleanup remove failed for %q: %v", relPath, removeErr)
		}
	}
}

func (s *PackagePoolIntegrationSuite) TestImportVerifyListOpenRemoveAcrossArtifactTypes(c *C) {
	initial, err := s.pool.FilepathList(nil)
	c.Assert(err, IsNil)
	c.Assert(initial, DeepEquals, []string{})

	imported := make([]string, 0, len(s.artifacts))

	for _, artifact := range s.artifacts {
		sourceChecksums, err := utils.ChecksumsForFile(artifact.path)
		c.Assert(err, IsNil)

		importChecksums := utils.ChecksumInfo{}
		poolPath, err := s.pool.Import(artifact.path, filepath.Base(artifact.path), &importChecksums, false, s.checksums)
		c.Assert(err, IsNil)
		imported = append(imported, poolPath)
		c.Assert(importChecksums.SHA256, Equals, sourceChecksums.SHA256)
		c.Assert(importChecksums.SHA512, Equals, sourceChecksums.SHA512)
		c.Assert(importChecksums.MD5, Equals, sourceChecksums.MD5)
		c.Assert(importChecksums.Size, Equals, sourceChecksums.Size)

		verifyChecksums := utils.ChecksumInfo{Size: sourceChecksums.Size}
		verifiedPath, exists, err := s.pool.Verify(poolPath, filepath.Base(artifact.path), &verifyChecksums, s.checksums)
		c.Assert(err, IsNil)
		c.Assert(exists, Equals, true)
		c.Assert(verifiedPath, Equals, poolPath)
		c.Assert(verifyChecksums.SHA256, Equals, sourceChecksums.SHA256)
		c.Assert(verifyChecksums.SHA512, Equals, sourceChecksums.SHA512)
		c.Assert(verifyChecksums.MD5, Equals, sourceChecksums.MD5)
		c.Assert(verifyChecksums.Size, Equals, sourceChecksums.Size)

		opened, err := s.pool.Open(poolPath)
		c.Assert(err, IsNil)
		payload, err := io.ReadAll(opened)
		c.Assert(err, IsNil)
		c.Assert(opened.Close(), IsNil)

		expectedPayload, err := os.ReadFile(artifact.path)
		c.Assert(err, IsNil)
		c.Assert(payload, DeepEquals, expectedPayload)
	}

	list, err := s.pool.FilepathList(nil)
	c.Assert(err, IsNil)

	expectedList := append([]string(nil), imported...)
	sort.Strings(expectedList)
	c.Assert(list, DeepEquals, expectedList)

	for _, relPath := range imported {
		size, err := s.pool.Remove(relPath)
		c.Assert(err, IsNil)
		stored := s.checksums.Store[relPath]
		c.Assert(size, Equals, stored.Size)
	}

	remaining, err := s.pool.FilepathList(nil)
	c.Assert(err, IsNil)
	c.Assert(remaining, DeepEquals, []string{})
}
