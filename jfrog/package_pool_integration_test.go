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
	url := strings.TrimSpace(os.Getenv("JFROG_URL"))
	repository := strings.TrimSpace(os.Getenv("JFROG_REPOSITORY"))
	username := strings.TrimSpace(os.Getenv("JFROG_USERNAME"))
	password := strings.TrimSpace(os.Getenv("JFROG_PASSWORD"))
	apiKey := strings.TrimSpace(os.Getenv("JFROG_APIKEY"))
	accessToken := strings.TrimSpace(os.Getenv("JFROG_ACCESSTOKEN"))

	hasUserPass := username != "" && password != ""
	hasAPIKey := apiKey != ""
	hasAccessToken := accessToken != ""

	if url == "" || repository == "" || (!hasUserPass && !hasAPIKey && !hasAccessToken) {
		c.Skip("skipping JFrog integration tests: missing URL, repository, or supported credentials")
	}
}

func (s *PackagePoolIntegrationSuite) SetUpTest(c *C) {
	_, currentFile, _, ok := runtime.Caller(0)
	c.Assert(ok, Equals, true)

	prefix := "aptly-pool-integration-" + strconv.FormatInt(time.Now().UnixNano(), 10)

	pool, err := NewPackagePool(utils.JFrogPoolStorage{
		Repository:  os.Getenv("JFROG_REPOSITORY"),
		URL:         os.Getenv("JFROG_URL"),
		User:        os.Getenv("JFROG_USERNAME"),
		Password:    os.Getenv("JFROG_PASSWORD"),
		APIKey:      os.Getenv("JFROG_APIKEY"),
		AccessToken: os.Getenv("JFROG_ACCESSTOKEN"),
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
