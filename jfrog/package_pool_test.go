package jfrog

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/aptly-dev/aptly/aptly"
	"github.com/aptly-dev/aptly/files"
	"github.com/aptly-dev/aptly/utils"
	"github.com/jfrog/jfrog-client-go/artifactory"
	jfrogutils "github.com/jfrog/jfrog-client-go/artifactory/services/utils"
	"github.com/jfrog/jfrog-client-go/config"
	"github.com/jfrog/jfrog-client-go/utils/io/content"
	. "gopkg.in/check.v1"
)

type PackagePoolSuite struct {
	manager      *fakeJFrogManager
	pool         *PackagePool
	debFile      string
	debBytes     []byte
	debChecksums utils.ChecksumInfo
	cs           aptly.ChecksumStorage
}

var _ = Suite(&PackagePoolSuite{})

type controlledChecksumStorage struct {
	store       map[string]utils.ChecksumInfo
	updateErr   error
	updateCalls int
}

func newControlledChecksumStorage() *controlledChecksumStorage {
	return &controlledChecksumStorage{store: map[string]utils.ChecksumInfo{}}
}

func (s *controlledChecksumStorage) Get(poolPath string) (*utils.ChecksumInfo, error) {
	stored, ok := s.store[poolPath]
	if !ok {
		return nil, nil
	}

	copy := stored
	return &copy, nil
}

func (s *controlledChecksumStorage) Update(poolPath string, checksums *utils.ChecksumInfo) error {
	s.updateCalls++
	if s.updateErr != nil {
		return s.updateErr
	}

	s.store[poolPath] = *checksums
	return nil
}

type failingCloseReadSeeker struct {
	*bytes.Reader
	err error
}

func (r *failingCloseReadSeeker) Close() error {
	return r.err
}

func (s *PackagePoolSuite) SetUpTest(c *C) {
	s.manager = &fakeJFrogManager{}
	s.pool = &PackagePool{
		readManager:  s.manager,
		writeManager: s.manager,
		repository:   "repo",
		prefix:       "prefix",
	}
	_, currentFile, _, _ := runtime.Caller(0)
	s.debFile = filepath.Join(filepath.Dir(currentFile), "../system/files/libboost-program-options-dev_1.49.0.1_i386.deb")

	var err error
	s.debBytes, err = os.ReadFile(s.debFile)
	c.Assert(err, IsNil)

	s.debChecksums, err = utils.ChecksumsForFile(s.debFile)
	c.Assert(err, IsNil)

	s.cs = files.NewMockChecksumStorage()
}

func (s *PackagePoolSuite) TestNewPackagePoolValidation(c *C) {
	_, err := NewPackagePool(utils.JFrogPoolStorage{Repository: "repo"}, 0, 0)
	c.Assert(err, ErrorMatches, "jfrog package pool url is required")

	_, err = NewPackagePool(utils.JFrogPoolStorage{URL: "https://example.jfrog.io/artifactory"}, 0, 0)
	c.Assert(err, ErrorMatches, "jfrog package pool repository is required")
}

func (s *PackagePoolSuite) TestNewPackagePoolCreatesManagersWithExplicitRetries(c *C) {
	previousFactory := newArtifactoryManager
	defer func() { newArtifactoryManager = previousFactory }()

	configs := []config.Config{}
	newArtifactoryManager = func(serviceConfig config.Config) (artifactory.ArtifactoryServicesManager, error) {
		configs = append(configs, serviceConfig)
		return &fakeJFrogManager{}, nil
	}

	pool, err := NewPackagePool(utils.JFrogPoolStorage{
		Repository: "repo",
		URL:        "https://example.jfrog.io/artifactory",
		User:       "user",
		Password:   "password",
	}, 2, 5)
	c.Assert(err, IsNil)
	c.Assert(pool, NotNil)
	c.Assert(configs, HasLen, 2)
	c.Check(configs[0].GetHttpRetries(), Equals, 2)
	c.Check(configs[1].GetHttpRetries(), Equals, 5)
}

func (s *PackagePoolSuite) TestSearchAndOpenUseReadManager(c *C) {
	readManager := &fakeJFrogManager{searchReader: createReader(c, []jfrogutils.ResultItem{})}
	writeManager := &fakeJFrogManager{}
	pool := &PackagePool{
		readManager:  readManager,
		writeManager: writeManager,
		repository:   "repo",
		prefix:       "prefix",
	}

	_, err := pool.FilepathList(nil)
	c.Assert(err, IsNil)
	_, err = pool.Open("path/package.deb")
	c.Assert(err, IsNil)
	c.Assert(readManager.searchParams, HasLen, 1)
	c.Assert(readManager.downloadParams, HasLen, 1)
	c.Assert(writeManager.searchParams, HasLen, 0)
	c.Assert(writeManager.downloadParams, HasLen, 0)
}

func (s *PackagePoolSuite) TestImportUsesWriteManager(c *C) {
	readManager := &fakeJFrogManager{searchReader: createReader(c, []jfrogutils.ResultItem{})}
	writeManager := &fakeJFrogManager{}
	pool := &PackagePool{
		readManager:  readManager,
		writeManager: writeManager,
		repository:   "repo",
		prefix:       "prefix",
	}
	checksums := utils.ChecksumInfo{}

	_, err := pool.Import(s.debFile, filepath.Base(s.debFile), &checksums, false, s.cs)
	c.Assert(err, IsNil)
	c.Assert(readManager.searchParams, HasLen, 1)
	c.Assert(readManager.uploadParams, HasLen, 0)
	c.Assert(writeManager.uploadParams, HasLen, 1)
}

func (s *PackagePoolSuite) TestRemoveUsesReadAndWriteManagers(c *C) {
	relPath := "c7/6b/4bd12fd92e4dfe1b55b18a67a669_package.deb"
	readManager := &fakeJFrogManager{
		searchReader:     createReader(c, []jfrogutils.ResultItem{{Repo: "repo", Path: "prefix/c7/6b", Name: "4bd12fd92e4dfe1b55b18a67a669_package.deb", Size: 2738}}),
		getPathsToDelete: createReader(c, []jfrogutils.ResultItem{}),
	}
	writeManager := &fakeJFrogManager{}
	pool := &PackagePool{
		readManager:  readManager,
		writeManager: writeManager,
		repository:   "repo",
		prefix:       "prefix",
	}

	_, err := pool.Remove(relPath)
	c.Assert(err, IsNil)
	c.Assert(readManager.deleteParams, HasLen, 1)
	c.Assert(readManager.deleteCalled, Equals, false)
	c.Assert(writeManager.deleteParams, HasLen, 0)
	c.Assert(writeManager.deleteCalled, Equals, true)
}

func (s *PackagePoolSuite) TestLegacyPath(c *C) {
	checksums := &utils.ChecksumInfo{SHA256: "c76b4bd12fd92e4dfe1b55b18a67a669d92f62985d6a96c8a21d96120982cf12"}

	p, err := s.pool.LegacyPath("dir/package.deb", checksums)
	c.Assert(err, ErrorMatches, "JFrog package pool does not support legacy paths")
	c.Assert(p, Equals, "")
}

func (s *PackagePoolSuite) TestImportCalculatesChecksumsAndStoresMetadata(c *C) {
	checksums := utils.ChecksumInfo{}
	s.manager.searchReader = createReader(c, []jfrogutils.ResultItem{})

	poolPath, err := s.pool.Import(s.debFile, filepath.Base(s.debFile), &checksums, false, s.cs)
	c.Assert(err, IsNil)
	c.Assert(poolPath, Equals, "c7/6b/4bd12fd92e4dfe1b55b18a67a669_libboost-program-options-dev_1.49.0.1_i386.deb")
	c.Assert(len(s.manager.uploadParams), Equals, 1)
	c.Assert(s.manager.uploadParams[0].Pattern, Equals, s.debFile)
	c.Assert(s.manager.uploadParams[0].Target, Equals, path.Join("repo", "prefix", poolPath))
	c.Assert(s.manager.uploadParams[0].Flat, Equals, true)
	c.Assert(checksums.SHA256, Equals, s.debChecksums.SHA256)
	c.Assert(checksums.SHA512, Equals, s.debChecksums.SHA512)

	stored := s.cs.(*files.MockChecksumStorage).Store[poolPath]
	c.Assert(stored.SHA256, Equals, s.debChecksums.SHA256)
	c.Assert(stored.SHA512, Equals, s.debChecksums.SHA512)
}

func (s *PackagePoolSuite) TestImportDeduplicatesWithoutSecondUpload(c *C) {
	firstChecksums := utils.ChecksumInfo{}
	s.manager.searchReader = createReader(c, []jfrogutils.ResultItem{})
	poolPath, err := s.pool.Import(s.debFile, filepath.Base(s.debFile), &firstChecksums, false, s.cs)
	c.Assert(err, IsNil)
	c.Assert(len(s.manager.uploadParams), Equals, 1)
	s.manager.searchReader = createReader(c, []jfrogutils.ResultItem{{
		Repo: "repo",
		Path: path.Join("prefix", "c7", "6b"),
		Name: "4bd12fd92e4dfe1b55b18a67a669_libboost-program-options-dev_1.49.0.1_i386.deb",
		Size: 2738,
	}})

	secondChecksums := utils.ChecksumInfo{}
	poolPathAgain, err := s.pool.Import(s.debFile, filepath.Base(s.debFile), &secondChecksums, false, s.cs)
	c.Assert(err, IsNil)
	c.Assert(poolPathAgain, Equals, poolPath)
	c.Assert(len(s.manager.uploadParams), Equals, 1)
	c.Assert(secondChecksums.SHA512, Equals, s.debChecksums.SHA512)
}

func (s *PackagePoolSuite) TestImportUploadsWhenStorageHasChecksumsButRemoteObjectIsMissing(c *C) {
	checksums := utils.ChecksumInfo{}
	s.manager.searchReader = createReader(c, []jfrogutils.ResultItem{})
	poolPath, err := s.pool.buildPoolPath(filepath.Base(s.debFile), &s.debChecksums)
	c.Assert(err, IsNil)

	s.cs.(*files.MockChecksumStorage).Store[poolPath] = utils.ChecksumInfo{
		Size:   s.debChecksums.Size,
		MD5:    "stale-md5",
		SHA256: s.debChecksums.SHA256,
		SHA512: "stale-sha512",
	}

	importPath, importErr := s.pool.Import(s.debFile, filepath.Base(s.debFile), &checksums, false, s.cs)
	c.Assert(importErr, IsNil)
	c.Assert(importPath, Equals, poolPath)
	c.Assert(len(s.manager.uploadParams), Equals, 1)
	c.Assert(s.manager.uploadParams[0].Target, Equals, path.Join("repo", "prefix", poolPath))
	c.Assert(checksums.MD5, Equals, s.debChecksums.MD5)
	c.Assert(checksums.SHA256, Equals, s.debChecksums.SHA256)
	c.Assert(checksums.SHA512, Equals, s.debChecksums.SHA512)

	stored := s.cs.(*files.MockChecksumStorage).Store[poolPath]
	c.Assert(stored.MD5, Equals, s.debChecksums.MD5)
	c.Assert(stored.SHA256, Equals, s.debChecksums.SHA256)
	c.Assert(stored.SHA512, Equals, s.debChecksums.SHA512)
}

func (s *PackagePoolSuite) TestImportRecalculatesFromRemoteWhenStorageMisses(c *C) {
	checksums := utils.ChecksumInfo{}
	s.manager.searchReader = createReader(c, []jfrogutils.ResultItem{})
	poolPath, err := s.pool.Import(s.debFile, filepath.Base(s.debFile), &checksums, false, s.cs)
	c.Assert(err, IsNil)

	delete(s.cs.(*files.MockChecksumStorage).Store, poolPath)
	s.manager.searchQueue = []*content.ContentReader{
		createReader(c, []jfrogutils.ResultItem{{
			Repo: "repo",
			Path: path.Join("prefix", "c7", "6b"),
			Name: "4bd12fd92e4dfe1b55b18a67a669_libboost-program-options-dev_1.49.0.1_i386.deb",
			Size: 2738,
		}}),
		createReader(c, []jfrogutils.ResultItem{{
			Repo: "repo",
			Path: path.Join("prefix", "c7", "6b"),
			Name: "4bd12fd92e4dfe1b55b18a67a669_libboost-program-options-dev_1.49.0.1_i386.deb",
			Size: 2738,
		}}),
	}
	s.manager.downloadContent = s.debBytes

	reimportChecksums := utils.ChecksumInfo{}
	reimportPath, err := s.pool.Import(s.debFile, filepath.Base(s.debFile), &reimportChecksums, false, s.cs)
	c.Assert(err, IsNil)
	c.Assert(reimportPath, Equals, poolPath)
	c.Assert(len(s.manager.uploadParams), Equals, 1)
	stored := s.cs.(*files.MockChecksumStorage).Store[poolPath]
	c.Assert(stored.MD5, Equals, s.debChecksums.MD5)
	c.Assert(stored.SHA256, Equals, s.debChecksums.SHA256)
	c.Assert(stored.SHA512, Equals, s.debChecksums.SHA512)
	c.Assert(stored.Size, Equals, s.debChecksums.Size)
	c.Assert(reimportChecksums.SHA512, Equals, s.debChecksums.SHA512)
	c.Assert(len(s.manager.downloadParams), Equals, 1)
	c.Assert(s.manager.downloadParams[0].Pattern, Equals, path.Join("repo", "prefix", poolPath))
}

func (s *PackagePoolSuite) TestImportUploadCountMustReportSingleSuccess(c *C) {
	checksums := utils.ChecksumInfo{}
	s.manager.searchReader = createReader(c, []jfrogutils.ResultItem{})
	s.manager.uploadSet = true
	s.manager.uploadCount = 0

	poolPath, err := s.pool.Import(s.debFile, filepath.Base(s.debFile), &checksums, false, s.cs)
	c.Assert(poolPath, Equals, "")
	c.Assert(err, ErrorMatches, "error uploading .* expected uploaded=1 and failed=0, got uploaded=0 failed=0")

	computedPoolPath, buildErr := s.pool.buildPoolPath(filepath.Base(s.debFile), &s.debChecksums)
	c.Assert(buildErr, IsNil)
	_, ok := s.cs.(*files.MockChecksumStorage).Store[computedPoolPath]
	c.Assert(ok, Equals, false)
}

func (s *PackagePoolSuite) TestImportUploadWithFailuresReturnsError(c *C) {
	checksums := utils.ChecksumInfo{}
	s.manager.searchReader = createReader(c, []jfrogutils.ResultItem{})
	s.manager.uploadSet = true
	s.manager.uploadCount = 1
	s.manager.uploadFailed = 1

	poolPath, err := s.pool.Import(s.debFile, filepath.Base(s.debFile), &checksums, false, s.cs)
	c.Assert(poolPath, Equals, "")
	c.Assert(err, ErrorMatches, "error uploading .* expected uploaded=1 and failed=0, got uploaded=1 failed=1")

	computedPoolPath, buildErr := s.pool.buildPoolPath(filepath.Base(s.debFile), &s.debChecksums)
	c.Assert(buildErr, IsNil)
	_, ok := s.cs.(*files.MockChecksumStorage).Store[computedPoolPath]
	c.Assert(ok, Equals, false)
}

func (s *PackagePoolSuite) TestImportUploadManagerErrorReturnsContextualError(c *C) {
	checksums := utils.ChecksumInfo{}
	s.manager.searchReader = createReader(c, []jfrogutils.ResultItem{})
	s.manager.uploadErr = errors.New("upload failed")

	poolPath, err := s.pool.Import(s.debFile, filepath.Base(s.debFile), &checksums, false, s.cs)
	c.Assert(poolPath, Equals, "")
	c.Assert(err, ErrorMatches, "error uploading .* to jfrog package pool key .* upload failed")
}

func (s *PackagePoolSuite) TestImportFailsWhenChecksumStorageUpdateFailsAfterUpload(c *C) {
	storage := newControlledChecksumStorage()
	storage.updateErr = errors.New("storage write failed")
	checksums := utils.ChecksumInfo{}
	s.manager.searchReader = createReader(c, []jfrogutils.ResultItem{})

	poolPath, err := s.pool.Import(s.debFile, filepath.Base(s.debFile), &checksums, false, storage)
	c.Assert(poolPath, Equals, "")
	c.Assert(err, ErrorMatches, "error storing checksums for .* storage write failed")
	c.Assert(len(storage.store), Equals, 0)
}

func (s *PackagePoolSuite) TestImportErrorsWhenRemoteSearchReturnsMultipleObjects(c *C) {
	checksums := utils.ChecksumInfo{SHA256: s.debChecksums.SHA256}
	s.manager.searchReader = createReader(c, []jfrogutils.ResultItem{
		{Repo: "repo", Path: path.Join("prefix", "c7", "6b"), Name: "a.deb", Size: 2738},
		{Repo: "repo", Path: path.Join("prefix", "c7", "6b"), Name: "b.deb", Size: 2738},
	})

	poolPath, err := s.pool.Import(s.debFile, filepath.Base(s.debFile), &checksums, false, s.cs)
	c.Assert(poolPath, Equals, "")
	c.Assert(err, ErrorMatches, "error searching jfrog package pool key .* expected exactly one search result for .* got 2")
	c.Assert(len(s.manager.uploadParams), Equals, 0)
}

func (s *PackagePoolSuite) TestImportSkipsChecksumUpdateWhenRecalculationCloseFails(c *C) {
	poolPath, err := s.pool.buildPoolPath(filepath.Base(s.debFile), &s.debChecksums)
	c.Assert(err, IsNil)

	storage := newControlledChecksumStorage()
	s.pool.openFn = func(relPath string) (aptly.ReadSeekerCloser, error) {
		c.Assert(relPath, Equals, poolPath)
		return &failingCloseReadSeeker{
			Reader: bytes.NewReader(s.debBytes),
			err:    errors.New("close failed"),
		}, nil
	}
	s.manager.searchQueue = []*content.ContentReader{
		createReader(c, []jfrogutils.ResultItem{{
			Repo: "repo",
			Path: path.Join("prefix", "c7", "6b"),
			Name: "4bd12fd92e4dfe1b55b18a67a669_libboost-program-options-dev_1.49.0.1_i386.deb",
			Size: 2738,
		}}),
		createReader(c, []jfrogutils.ResultItem{{
			Repo: "repo",
			Path: path.Join("prefix", "c7", "6b"),
			Name: "4bd12fd92e4dfe1b55b18a67a669_libboost-program-options-dev_1.49.0.1_i386.deb",
			Size: 2738,
		}}),
	}

	checksums := utils.ChecksumInfo{SHA256: s.debChecksums.SHA256}
	importPath, importErr := s.pool.Import(s.debFile, filepath.Base(s.debFile), &checksums, false, storage)
	c.Assert(importPath, Equals, "")
	c.Assert(importErr, ErrorMatches, "error closing jfrog package pool reader for .* close failed")
	c.Assert(storage.updateCalls, Equals, 0)
	c.Assert(len(storage.store), Equals, 0)
	c.Assert(len(s.manager.uploadParams), Equals, 0)
}

func (s *PackagePoolSuite) TestBuildPoolPath(c *C) {
	p, err := s.pool.buildPoolPath("package.deb", &utils.ChecksumInfo{SHA256: "c76b4bd12fd92e4dfe1b55b18a67a669d92f62985d6a96c8a21d96120982cf12"})
	c.Assert(err, IsNil)
	c.Assert(p, Equals, "c7/6b/4bd12fd92e4dfe1b55b18a67a669_package.deb")

	_, err = s.pool.buildPoolPath("package.deb", &utils.ChecksumInfo{SHA256: "c76b4bd12fd92e4dfe1b55b18a67"})
	c.Assert(err, ErrorMatches, "unable to compute pool location for filename package.deb, SHA256 is missing")
}

func (s *PackagePoolSuite) TestVerifyFailuresAndSuccess(c *C) {
	importChecksums := utils.ChecksumInfo{}
	s.manager.searchReader = createReader(c, []jfrogutils.ResultItem{})
	poolPath, err := s.pool.Import(s.debFile, filepath.Base(s.debFile), &importChecksums, false, s.cs)
	c.Assert(err, IsNil)

	verifiedPath, exists, err := s.pool.Verify("", filepath.Base(s.debFile), &utils.ChecksumInfo{}, s.cs)
	c.Assert(err, IsNil)
	c.Assert(exists, Equals, false)
	c.Assert(verifiedPath, Equals, "")

	s.manager.searchReader = createReader(c, []jfrogutils.ResultItem{{
		Repo: "repo",
		Path: path.Join("prefix", "c7", "6b"),
		Name: "4bd12fd92e4dfe1b55b18a67a669_libboost-program-options-dev_1.49.0.1_i386.deb",
		Size: 2738,
	}})
	verifiedPath, exists, err = s.pool.Verify(poolPath, filepath.Base(s.debFile), &utils.ChecksumInfo{Size: 0}, s.cs)
	c.Assert(err, IsNil)
	c.Assert(exists, Equals, false)
	c.Assert(verifiedPath, Equals, "")

	s.manager.searchReader = createReader(c, []jfrogutils.ResultItem{{
		Repo: "repo",
		Path: path.Join("prefix", "c7", "6b"),
		Name: "4bd12fd92e4dfe1b55b18a67a669_libboost-program-options-dev_1.49.0.1_i386.deb",
		Size: 2738,
	}})
	verifiedPath, exists, err = s.pool.Verify(poolPath, filepath.Base(s.debFile), &utils.ChecksumInfo{
		Size: importChecksums.Size,
		MD5:  "deadbeef",
	}, s.cs)
	c.Assert(err, IsNil)
	c.Assert(exists, Equals, false)
	c.Assert(verifiedPath, Equals, "")

	s.manager.searchReader = createReader(c, []jfrogutils.ResultItem{{
		Repo: "repo",
		Path: path.Join("prefix", "c7", "6b"),
		Name: "4bd12fd92e4dfe1b55b18a67a669_libboost-program-options-dev_1.49.0.1_i386.deb",
		Size: 2738,
	}})
	verifiedPath, exists, err = s.pool.Verify(poolPath, filepath.Base(s.debFile), &utils.ChecksumInfo{
		Size:   importChecksums.Size,
		SHA256: "deadbeef",
	}, s.cs)
	c.Assert(err, IsNil)
	c.Assert(exists, Equals, false)
	c.Assert(verifiedPath, Equals, "")

	checksums := utils.ChecksumInfo{Size: importChecksums.Size}
	s.manager.searchReader = createReader(c, []jfrogutils.ResultItem{{
		Repo: "repo",
		Path: path.Join("prefix", "c7", "6b"),
		Name: "4bd12fd92e4dfe1b55b18a67a669_libboost-program-options-dev_1.49.0.1_i386.deb",
		Size: 2738,
	}})
	verifiedPath, exists, err = s.pool.Verify(poolPath, filepath.Base(s.debFile), &checksums, s.cs)
	c.Assert(err, IsNil)
	c.Assert(exists, Equals, true)
	c.Assert(verifiedPath, Equals, poolPath)
	c.Assert(checksums.Size, Equals, s.debChecksums.Size)
	c.Assert(checksums.MD5, Equals, s.debChecksums.MD5)
	c.Assert(checksums.SHA256, Equals, s.debChecksums.SHA256)
	c.Assert(checksums.SHA512, Equals, s.debChecksums.SHA512)
}

func (s *PackagePoolSuite) TestVerifyMissingObjectReturnsLookupError(c *C) {
	poolPath, err := s.pool.buildPoolPath(filepath.Base(s.debFile), &s.debChecksums)
	c.Assert(err, IsNil)

	s.manager.searchReader = createReader(c, []jfrogutils.ResultItem{})
	verifiedPath, exists, verifyErr := s.pool.Verify(poolPath, filepath.Base(s.debFile), &utils.ChecksumInfo{Size: s.debChecksums.Size}, s.cs)
	c.Assert(verifiedPath, Equals, "")
	c.Assert(exists, Equals, false)
	c.Assert(verifyErr, IsNil)
}

func (s *PackagePoolSuite) TestVerifyRecalculatesAndReplacesChecksumsWhenStorageIsEmpty(c *C) {
	importChecksums := utils.ChecksumInfo{}
	s.manager.searchReader = createReader(c, []jfrogutils.ResultItem{})
	poolPath, err := s.pool.Import(s.debFile, filepath.Base(s.debFile), &importChecksums, false, s.cs)
	c.Assert(err, IsNil)

	delete(s.cs.(*files.MockChecksumStorage).Store, poolPath)

	s.manager.searchQueue = []*content.ContentReader{
		createReader(c, []jfrogutils.ResultItem{{
			Repo: "repo",
			Path: path.Join("prefix", "c7", "6b"),
			Name: "4bd12fd92e4dfe1b55b18a67a669_libboost-program-options-dev_1.49.0.1_i386.deb",
			Size: 2738,
		}}),
		createReader(c, []jfrogutils.ResultItem{{
			Repo: "repo",
			Path: path.Join("prefix", "c7", "6b"),
			Name: "4bd12fd92e4dfe1b55b18a67a669_libboost-program-options-dev_1.49.0.1_i386.deb",
			Size: 2738,
		}}),
	}
	s.manager.downloadContent = s.debBytes

	checksums := utils.ChecksumInfo{
		Size: s.debChecksums.Size,
	}

	verifiedPath, exists, verifyErr := s.pool.Verify(poolPath, filepath.Base(s.debFile), &checksums, s.cs)
	c.Assert(verifyErr, IsNil)
	c.Assert(verifiedPath, Equals, poolPath)
	c.Assert(exists, Equals, true)
	c.Assert(checksums.Size, Equals, s.debChecksums.Size)
	c.Assert(checksums.MD5, Equals, s.debChecksums.MD5)
	c.Assert(checksums.SHA256, Equals, s.debChecksums.SHA256)
	c.Assert(checksums.SHA512, Equals, s.debChecksums.SHA512)

	stored := s.cs.(*files.MockChecksumStorage).Store[poolPath]
	c.Assert(stored.Size, Equals, s.debChecksums.Size)
	c.Assert(stored.MD5, Equals, s.debChecksums.MD5)
	c.Assert(stored.SHA256, Equals, s.debChecksums.SHA256)
	c.Assert(stored.SHA512, Equals, s.debChecksums.SHA512)
	c.Assert(len(s.manager.downloadParams), Equals, 1)
	c.Assert(s.manager.downloadParams[0].Pattern, Equals, path.Join("repo", "prefix", poolPath))
}

func (s *PackagePoolSuite) TestSize(c *C) {
	rel := "c7/6b/4bd12fd92e4dfe1b55b18a67a669_package.deb"
	s.manager.searchReader = createReader(c, []jfrogutils.ResultItem{{
		Repo: "repo",
		Path: path.Join("prefix", "c7", "6b"),
		Name: "4bd12fd92e4dfe1b55b18a67a669_package.deb",
		Size: 2738,
	}})

	size, err := s.pool.Size(rel)
	c.Assert(err, IsNil)
	c.Assert(size, Equals, int64(2738))
	c.Assert(s.manager.searchParams[0].Pattern, Equals, path.Join("repo", "prefix", rel))
	c.Assert(s.manager.searchParams[0].Recursive, Equals, false)

	s.manager.searchReader = createReader(c, []jfrogutils.ResultItem{})
	_, err = s.pool.Size(rel)
	c.Assert(err, ErrorMatches, "expected exactly one search result.*")

	s.manager.searchReader = createReader(c, []jfrogutils.ResultItem{
		{Repo: "repo", Path: path.Join("prefix", "c7", "6b"), Name: "a.deb", Size: 1},
		{Repo: "repo", Path: path.Join("prefix", "c7", "6b"), Name: "b.deb", Size: 2},
	})
	_, err = s.pool.Size(rel)
	c.Assert(err, ErrorMatches, "expected exactly one search result.*")
}

func (s *PackagePoolSuite) TestFilepathList(c *C) {
	s.manager.searchReader = createReader(c, []jfrogutils.ResultItem{
		{Repo: "repo", Path: path.Join("prefix", "x", "y"), Name: "z.deb"},
		{Repo: "repo", Path: path.Join("prefix", "a", "b"), Name: "c.deb"},
		{Repo: "repo", Path: "prefix", Name: "top.deb"},
	})

	list, err := s.pool.FilepathList(nil)
	c.Assert(err, IsNil)
	c.Assert(list, DeepEquals, []string{"a/b/c.deb", "top.deb", "x/y/z.deb"})
	c.Assert(s.manager.searchParams[0].Pattern, Equals, path.Join("repo", "prefix", "*"))
	c.Assert(s.manager.searchParams[0].Recursive, Equals, true)
}

func (s *PackagePoolSuite) TestFilepathListRootPath(c *C) {
	s.pool.prefix = ""
	s.manager.searchReader = createReader(c, []jfrogutils.ResultItem{
		{Repo: "repo", Path: ".", Name: "at-root.deb"},
	})

	list, err := s.pool.FilepathList(nil)
	c.Assert(err, IsNil)
	c.Assert(list, DeepEquals, []string{"at-root.deb"})
}

func (s *PackagePoolSuite) TestFilepathListRejectsOutsidePrefix(c *C) {
	s.manager.searchReader = createReader(c, []jfrogutils.ResultItem{
		{Repo: "repo", Path: path.Join("another-prefix", "a"), Name: "b.deb"},
	})

	_, err := s.pool.FilepathList(nil)
	c.Assert(err, ErrorMatches, `search result path .* is outside configured pool prefix .*`)
}

func (s *PackagePoolSuite) TestRemove(c *C) {
	rel := "c7/6b/4bd12fd92e4dfe1b55b18a67a669_package.deb"
	s.manager.searchReader = createReader(c, []jfrogutils.ResultItem{{
		Repo: "repo",
		Path: path.Join("prefix", "c7", "6b"),
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
	c.Assert(strings.HasSuffix(s.manager.downloadParams[0].Target, string(os.PathSeparator)), Equals, true)

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

func (s *PackagePoolSuite) TestOpenDownloadErrorCleansTempTarget(c *C) {
	rel := "c7/6b/4bd12fd92e4dfe1b55b18a67a669_package.deb"
	s.manager.downloadErr = errors.New("download failed")

	reader, err := s.pool.Open(rel)
	c.Assert(reader, IsNil)
	c.Assert(err, ErrorMatches, "error downloading .* download failed")

	targetDir := s.manager.downloadParams[0].Target
	_, statErr := os.Stat(targetDir)
	c.Assert(os.IsNotExist(statErr), Equals, true)
}

func (s *PackagePoolSuite) TestOpenFailedDownloadCountCleansTempTarget(c *C) {
	rel := "c7/6b/4bd12fd92e4dfe1b55b18a67a669_package.deb"
	s.manager.downloadCountSet = true
	s.manager.downloadCount = 0
	s.manager.downloadFailed = 1

	reader, err := s.pool.Open(rel)
	c.Assert(reader, IsNil)
	c.Assert(err, ErrorMatches, `error downloading .*: 1 file\(s\) failed`)

	targetDir := s.manager.downloadParams[0].Target
	_, statErr := os.Stat(targetDir)
	c.Assert(os.IsNotExist(statErr), Equals, true)
}

func (s *PackagePoolSuite) TestOpenMissingDownloadedFileCleansTempTarget(c *C) {
	rel := "c7/6b/4bd12fd92e4dfe1b55b18a67a669_package.deb"
	s.manager.downloadCountSet = true
	s.manager.downloadCount = 1
	s.manager.downloadSkipWrite = true

	reader, err := s.pool.Open(rel)
	c.Assert(reader, IsNil)
	c.Assert(err, ErrorMatches, "error opening downloaded package .*")

	targetDir := s.manager.downloadParams[0].Target
	_, statErr := os.Stat(targetDir)
	c.Assert(os.IsNotExist(statErr), Equals, true)
}

func (s *PackagePoolSuite) TestOpenZeroDownloadedZeroFailed(c *C) {
	rel := "c7/6b/4bd12fd92e4dfe1b55b18a67a669_package.deb"
	s.manager.downloadCountSet = true
	s.manager.downloadCount = 0
	s.manager.downloadSkipWrite = true

	reader, err := s.pool.Open(rel)
	c.Assert(reader, IsNil)
	c.Assert(err, ErrorMatches, "error downloading .*: no files were downloaded")

	targetDir := s.manager.downloadParams[0].Target
	_, statErr := os.Stat(targetDir)
	c.Assert(os.IsNotExist(statErr), Equals, true)
}

func (s *PackagePoolSuite) TestSearchIterationErrorIsReturned(c *C) {
	s.manager.searchReader = createMalformedReader(c)

	_, err := s.pool.FilepathList(nil)
	c.Assert(err, ErrorMatches, "error reading jfrog search results:.*")
}
