package jfrog

import (
	"io"
	"os"
	"path"
	"path/filepath"

	"github.com/aptly-dev/aptly/utils"
	jfrogutils "github.com/jfrog/jfrog-client-go/artifactory/services/utils"
	. "gopkg.in/check.v1"
)

type PackagePoolSuite struct {
	manager *fakeJFrogManager
	pool    *PackagePool
}

var _ = Suite(&PackagePoolSuite{})

func (s *PackagePoolSuite) SetUpTest(_ *C) {
	s.manager = &fakeJFrogManager{}
	s.pool = &PackagePool{
		manager:    s.manager,
		repository: "repo",
		prefix:     "prefix",
	}
}

func (s *PackagePoolSuite) TestNewPackagePoolValidation(c *C) {
	_, err := NewPackagePool(utils.JFrogPoolStorage{Repository: "repo"})
	c.Assert(err, ErrorMatches, "jfrog package pool url is required")

	_, err = NewPackagePool(utils.JFrogPoolStorage{URL: "https://example.jfrog.io/artifactory"})
	c.Assert(err, ErrorMatches, "jfrog package pool repository is required")
}

func (s *PackagePoolSuite) TestLegacyPath(c *C) {
	checksums := &utils.ChecksumInfo{SHA256: "c76b4bd12fd92e4dfe1b55b18a67a669d92f62985d6a96c8a21d96120982cf12"}

	p, err := s.pool.LegacyPath("dir/package.deb", checksums)
	c.Assert(err, ErrorMatches, "JFrog package pool does not support legacy paths")
	c.Assert(p, Equals, "")
}

func (s *PackagePoolSuite) TestImportAndVerifyInterimErrors(c *C) {
	_, err := s.pool.Import("/tmp/source.deb", "package.deb", &utils.ChecksumInfo{}, false, nil)
	c.Assert(err, ErrorMatches, ".*not implemented.*")

	_, exists, err := s.pool.Verify("", "package.deb", &utils.ChecksumInfo{}, nil)
	c.Assert(err, ErrorMatches, ".*not implemented.*")
	c.Assert(exists, Equals, false)
}

func (s *PackagePoolSuite) TestSize(c *C) {
	rel := "c7/6b/4bd12fd92e4dfe1b55b18a67a669_package.deb"
	s.manager.searchReader = createReader(c, []jfrogutils.ResultItem{{
		Path: path.Join("repo", "prefix", "c7", "6b"),
		Name: "4bd12fd92e4dfe1b55b18a67a669_package.deb",
		Size: 2738,
	}})

	size, err := s.pool.Size(rel)
	c.Assert(err, IsNil)
	c.Assert(size, Equals, int64(2738))
	c.Assert(s.manager.searchParams[0].Pattern, Equals, path.Join("repo", "prefix", rel))

	s.manager.searchReader = createReader(c, []jfrogutils.ResultItem{})
	_, err = s.pool.Size(rel)
	c.Assert(err, ErrorMatches, "expected exactly one search result.*")

	s.manager.searchReader = createReader(c, []jfrogutils.ResultItem{
		{Path: path.Join("repo", "prefix", "c7", "6b"), Name: "a.deb", Size: 1},
		{Path: path.Join("repo", "prefix", "c7", "6b"), Name: "b.deb", Size: 2},
	})
	_, err = s.pool.Size(rel)
	c.Assert(err, ErrorMatches, "expected exactly one search result.*")
}

func (s *PackagePoolSuite) TestFilepathList(c *C) {
	s.manager.searchReader = createReader(c, []jfrogutils.ResultItem{
		{Path: path.Join("repo", "prefix", "x", "y"), Name: "z.deb"},
		{Path: path.Join("repo", "prefix", "a", "b"), Name: "c.deb"},
	})

	list, err := s.pool.FilepathList(nil)
	c.Assert(err, IsNil)
	c.Assert(list, DeepEquals, []string{"a/b/c.deb", "x/y/z.deb"})
	c.Assert(s.manager.searchParams[0].Pattern, Equals, path.Join("repo", "prefix", "*"))
}

func (s *PackagePoolSuite) TestRemove(c *C) {
	rel := "c7/6b/4bd12fd92e4dfe1b55b18a67a669_package.deb"
	s.manager.searchReader = createReader(c, []jfrogutils.ResultItem{{
		Path: path.Join("repo", "prefix", "c7", "6b"),
		Name: "4bd12fd92e4dfe1b55b18a67a669_package.deb",
		Size: 2738,
	}})
	s.manager.getPathsToDelete = createReader(c, []jfrogutils.ResultItem{})

	size, err := s.pool.Remove(rel)
	c.Assert(err, IsNil)
	c.Assert(size, Equals, int64(2738))
	c.Assert(s.manager.searchParams[0].Pattern, Equals, path.Join("repo", "prefix", rel))
	c.Assert(s.manager.deleteParams[0].Pattern, Equals, path.Join("repo", "prefix", rel))
	c.Assert(s.manager.deleteCalled, Equals, true)
}

func (s *PackagePoolSuite) TestOpen(c *C) {
	rel := "c7/6b/4bd12fd92e4dfe1b55b18a67a669_package.deb"
	s.manager.downloadContent = []byte("package-bytes")

	reader, err := s.pool.Open(rel)
	c.Assert(err, IsNil)
	c.Assert(len(s.manager.downloadParams), Equals, 1)
	c.Assert(s.manager.downloadParams[0].Pattern, Equals, path.Join("repo", "prefix", rel))
	c.Assert(s.manager.downloadParams[0].Flat, Equals, true)

	targetDir := s.manager.downloadParams[0].Target
	_, statErr := os.Stat(targetDir)
	c.Assert(statErr, IsNil)

	payload, err := io.ReadAll(reader)
	c.Assert(err, IsNil)
	c.Assert(string(payload), Equals, "package-bytes")

	c.Assert(reader.Close(), IsNil)
	_, statErr = os.Stat(targetDir)
	c.Assert(os.IsNotExist(statErr), Equals, true)

	_, statErr = os.Stat(filepath.Join(targetDir, path.Base(rel)))
	c.Assert(os.IsNotExist(statErr), Equals, true)
}
