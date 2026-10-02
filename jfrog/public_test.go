package jfrog

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aptly-dev/aptly/aptly"
	aptly_utils "github.com/aptly-dev/aptly/utils"
	"github.com/jfrog/jfrog-client-go/artifactory"
	"github.com/jfrog/jfrog-client-go/artifactory/services"
	jfrogutils "github.com/jfrog/jfrog-client-go/artifactory/services/utils"
	"github.com/jfrog/jfrog-client-go/utils/io/content"
	. "gopkg.in/check.v1"
)

func Test(t *testing.T) {
	t.Setenv("JFROG_USERNAME", "userfromenv")
	TestingT(t)
}

type fakeJFrogManager struct {
	artifactory.EmptyArtifactoryServicesManager

	uploadParams []services.UploadParams
	uploadErr    error
	uploadCount  int
	uploadFailed int
	uploadSet    bool

	deleteParams      []services.DeleteParams
	getPathsToDelete  *content.ContentReader
	getPathsDeleteErr error
	deleteErr         error
	deleteCalled      bool

	searchParams []services.SearchParams
	searchReader *content.ContentReader
	searchQueue  []*content.ContentReader
	searchErr    error

	downloadParams    []services.DownloadParams
	downloadErr       error
	downloadContent   []byte
	downloadFailed    int
	downloadCount     int
	downloadCountSet  bool
	downloadSkipWrite bool

	moveParams []services.MoveCopyParams
	moveErr    error

	copyParams []services.MoveCopyParams
	copyErr    error
	copyCount  int
	copyFailed int
	copySet    bool

	itemProps    *jfrogutils.ItemProperties
	itemPropsErr error
}

func (m *fakeJFrogManager) UploadFiles(_ artifactory.UploadServiceOptions, params ...services.UploadParams) (int, int, error) {
	m.uploadParams = append(m.uploadParams, params...)
	uploaded := len(params)
	if m.uploadSet {
		uploaded = m.uploadCount
	}

	return uploaded, m.uploadFailed, m.uploadErr
}

func (m *fakeJFrogManager) GetPathsToDelete(params services.DeleteParams) (*content.ContentReader, error) {
	m.deleteParams = append(m.deleteParams, params)
	if m.getPathsDeleteErr != nil {
		return nil, m.getPathsDeleteErr
	}
	if m.getPathsToDelete != nil {
		return m.getPathsToDelete, nil
	}
	return content.NewEmptyContentReader("results"), nil
}

func (m *fakeJFrogManager) DeleteFiles(_ *content.ContentReader) (int, error) {
	m.deleteCalled = true
	return 1, m.deleteErr
}

func (m *fakeJFrogManager) SearchFiles(params services.SearchParams) (*content.ContentReader, error) {
	m.searchParams = append(m.searchParams, params)
	if m.searchErr != nil {
		return nil, m.searchErr
	}
	if len(m.searchQueue) > 0 {
		reader := m.searchQueue[0]
		m.searchQueue = m.searchQueue[1:]
		return reader, nil
	}
	if m.searchReader != nil {
		return m.searchReader, nil
	}
	return content.NewEmptyContentReader("results"), nil
}

func (m *fakeJFrogManager) DownloadFiles(params ...services.DownloadParams) (int, int, error) {
	m.downloadParams = append(m.downloadParams, params...)

	data := m.downloadContent
	if data == nil {
		data = []byte("downloaded-bytes")
	}

	downloadedCount := len(params)
	if m.downloadCountSet {
		downloadedCount = m.downloadCount
	}

	for _, p := range params {
		target := strings.TrimSpace(p.Target)
		if target == "" {
			return 0, len(params), errors.New("download target is empty")
		}

		writePath := target
		if strings.HasSuffix(target, "/") || strings.HasSuffix(target, "\\") {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return 0, len(params), err
			}
			writePath = filepath.Join(target, path.Base(p.Pattern))
		} else {
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return 0, len(params), err
			}
		}

		if !m.downloadSkipWrite {
			if err := os.WriteFile(writePath, data, 0o644); err != nil {
				return 0, len(params), err
			}
		}
	}

	if m.downloadErr != nil {
		return 0, m.downloadFailed, m.downloadErr
	}
	if m.downloadFailed > 0 {
		return downloadedCount, m.downloadFailed, nil
	}

	return downloadedCount, 0, nil
}

func (m *fakeJFrogManager) Move(params ...services.MoveCopyParams) (int, int, error) {
	m.moveParams = append(m.moveParams, params...)
	return len(params), 0, m.moveErr
}

func (m *fakeJFrogManager) Copy(params ...services.MoveCopyParams) (int, int, error) {
	m.copyParams = append(m.copyParams, params...)
	copied := len(params)
	if m.copySet {
		copied = m.copyCount
	}
	return copied, m.copyFailed, m.copyErr
}

func (m *fakeJFrogManager) GetItemProps(_ string) (*jfrogutils.ItemProperties, error) {
	if m.itemPropsErr != nil {
		return nil, m.itemPropsErr
	}
	if m.itemProps != nil {
		return m.itemProps, nil
	}
	return &jfrogutils.ItemProperties{}, nil
}

type resultFixture struct {
	Results []jfrogutils.ResultItem `json:"results"`
}

type fakeLocalPool struct{}

func (p *fakeLocalPool) Verify(string, string, *aptly_utils.ChecksumInfo, aptly.ChecksumStorage) (string, bool, error) {
	return "", false, nil
}

func (p *fakeLocalPool) Import(string, string, *aptly_utils.ChecksumInfo, bool, aptly.ChecksumStorage) (string, error) {
	return "", nil
}

func (p *fakeLocalPool) LegacyPath(string, *aptly_utils.ChecksumInfo) (string, error) {
	return "", nil
}

func (p *fakeLocalPool) Size(string) (int64, error) {
	return 0, nil
}

func (p *fakeLocalPool) Open(string) (aptly.ReadSeekerCloser, error) {
	return nil, errors.New("not implemented")
}

func (p *fakeLocalPool) FilepathList(aptly.Progress) ([]string, error) {
	return nil, nil
}

func (p *fakeLocalPool) Remove(string) (int64, error) {
	return 0, nil
}

func (p *fakeLocalPool) Stat(string) (os.FileInfo, error) {
	return nil, errors.New("not implemented")
}

func (p *fakeLocalPool) GenerateTempPath(string) (string, error) {
	return "", nil
}

func (p *fakeLocalPool) Link(string, string) error {
	return nil
}

func (p *fakeLocalPool) Symlink(string, string) error {
	return nil
}

func (p *fakeLocalPool) FullPath(path string) string {
	return filepath.Join("/var/lib/aptly/pool", path)
}

type fakeRemotePool struct {
	openPath string
	openErr  error
}

func (p *fakeRemotePool) Verify(string, string, *aptly_utils.ChecksumInfo, aptly.ChecksumStorage) (string, bool, error) {
	return "", false, nil
}

func (p *fakeRemotePool) Import(string, string, *aptly_utils.ChecksumInfo, bool, aptly.ChecksumStorage) (string, error) {
	return "", nil
}

func (p *fakeRemotePool) LegacyPath(string, *aptly_utils.ChecksumInfo) (string, error) {
	return "", nil
}

func (p *fakeRemotePool) Size(string) (int64, error) {
	return 0, nil
}

func (p *fakeRemotePool) Open(string) (aptly.ReadSeekerCloser, error) {
	if p.openErr != nil {
		return nil, p.openErr
	}
	return os.Open(p.openPath)
}

func (p *fakeRemotePool) FilepathList(aptly.Progress) ([]string, error) {
	return nil, nil
}

func (p *fakeRemotePool) Remove(string) (int64, error) {
	return 0, nil
}

func createReader(c *C, results []jfrogutils.ResultItem) *content.ContentReader {
	filePath := filepath.Join(c.MkDir(), "results.json")
	data, err := json.Marshal(resultFixture{Results: results})
	c.Assert(err, IsNil)
	c.Assert(os.WriteFile(filePath, data, 0o644), IsNil)
	return content.NewContentReader(filePath, "results")
}

func createMalformedReader(c *C) *content.ContentReader {
	filePath := filepath.Join(c.MkDir(), "missing-results.json")
	return content.NewContentReader(filePath, "results")
}

type PublishedStorageSuite struct {
	manager *fakeJFrogManager
	storage *PublishedStorage
}

var _ = Suite(&PublishedStorageSuite{})

func (s *PublishedStorageSuite) SetUpTest(c *C) {
	s.manager = &fakeJFrogManager{}
	s.storage = &PublishedStorage{
		manager:    s.manager,
		repository: "repo",
		prefix:     "prefix",
	}
}

func (s *PublishedStorageSuite) TestStringAndMkDir(c *C) {
	c.Assert(s.storage.String(), Equals, "jfrog:repo:prefix")
	c.Assert(s.storage.MkDir("anything"), IsNil)
}

func (s *PublishedStorageSuite) TestPutFile(c *C) {
	err := s.storage.PutFile("pool/main/a+b.deb", "/tmp/source.deb")
	c.Assert(err, IsNil)
	c.Assert(len(s.manager.uploadParams), Equals, 1)
	c.Assert(s.manager.uploadParams[0].Pattern, Equals, "/tmp/source.deb")
	c.Assert(s.manager.uploadParams[0].Target, Equals, filepath.Join("repo", "prefix", "pool/main/a+b.deb"))
	c.Assert(s.manager.uploadParams[0].Flat, Equals, true)
}

func (s *PublishedStorageSuite) TestPutFilePlusWorkaroundAndError(c *C) {
	s.storage.plusWorkaround = true
	s.manager.uploadErr = errors.New("upload failed")

	err := s.storage.PutFile("pool/main/a+b.deb", "/tmp/source.deb")
	c.Assert(err, ErrorMatches, "upload failed")
	c.Assert(s.manager.uploadParams[0].Target, Equals, filepath.Join("repo", "prefix", "pool/main/a%2Bb.deb"))
}

func (s *PublishedStorageSuite) TestRemove(c *C) {
	s.manager.getPathsToDelete = createReader(c, []jfrogutils.ResultItem{})

	err := s.storage.Remove("dists/stable+main")
	c.Assert(err, IsNil)
	c.Assert(len(s.manager.deleteParams), Equals, 1)
	c.Assert(s.manager.deleteParams[0].Pattern, Equals, filepath.Join("repo", "prefix", "dists/stable+main"))
	c.Assert(s.manager.deleteCalled, Equals, true)
}

func (s *PublishedStorageSuite) TestRemovePlusWorkaround(c *C) {
	s.storage.plusWorkaround = true
	s.manager.getPathsToDelete = createReader(c, []jfrogutils.ResultItem{})

	err := s.storage.Remove("pool/a+b.deb")
	c.Assert(err, IsNil)
	c.Assert(s.manager.deleteParams[0].Pattern, Equals, filepath.Join("repo", "prefix", "pool/a%2Bb.deb"))
}

func (s *PublishedStorageSuite) TestRemoveErrors(c *C) {
	s.manager.getPathsDeleteErr = errors.New("search delete failed")
	err := s.storage.Remove("x")
	c.Assert(err, ErrorMatches, "search delete failed")

	s.manager.getPathsDeleteErr = nil
	s.manager.getPathsToDelete = createReader(c, []jfrogutils.ResultItem{})
	s.manager.deleteErr = errors.New("delete failed")
	err = s.storage.Remove("x")
	c.Assert(err, ErrorMatches, "delete failed")
}

func (s *PublishedStorageSuite) TestRemoveDirsDelegatesToRemove(c *C) {
	s.manager.getPathsToDelete = createReader(c, []jfrogutils.ResultItem{})
	c.Assert(s.storage.RemoveDirs("x", nil), IsNil)
	c.Assert(len(s.manager.deleteParams), Equals, 1)
}

func (s *PublishedStorageSuite) TestLinkFromPoolDelegatesToPutFile(c *C) {
	err := s.storage.LinkFromPool("", "pool/main/p", "pkg.deb", nil, "/tmp/source.deb", aptly_utils.ChecksumInfo{}, false)
	c.Assert(err, IsNil)
	c.Assert(s.manager.uploadParams[0].Target, Equals, path.Join("repo", "prefix", "pool/main/p", "pkg.deb"))
}

func (s *PublishedStorageSuite) TestLinkFromPoolUsesLocalPoolFullPath(c *C) {
	pool := &fakeLocalPool{}
	poolPath := "e3/48/84d71bb98002bf0c775479aa31ee_accountsservice_0.6.55-0ubuntu11_amd64.deb"

	err := s.storage.LinkFromPool("", "pool/main/p", "pkg.deb", pool, poolPath, aptly_utils.ChecksumInfo{}, false)
	c.Assert(err, IsNil)
	c.Assert(s.manager.uploadParams[0].Pattern, Equals, filepath.Join("/var/lib/aptly/pool", poolPath))
}

func (s *PublishedStorageSuite) TestLinkFromPoolCopiesFromRemotePool(c *C) {
	tmpFile := filepath.Join(c.MkDir(), "source.deb")
	c.Assert(os.WriteFile(tmpFile, []byte("package-bytes"), 0o644), IsNil)

	pool := &fakeRemotePool{openPath: tmpFile}
	err := s.storage.LinkFromPool("", "pool/main/p", "pkg.deb", pool, "hash/path/pkg.deb", aptly_utils.ChecksumInfo{}, false)
	c.Assert(err, IsNil)

	uploadPath := s.manager.uploadParams[0].Pattern
	c.Assert(uploadPath, Not(Equals), "hash/path/pkg.deb")
	_, statErr := os.Stat(uploadPath)
	c.Assert(os.IsNotExist(statErr), Equals, true)
}

func (s *PublishedStorageSuite) TestLinkFromMatchingJFrogPoolUsesServerSideCopy(c *C) {
	s.storage.url = normalizeArtifactoryURL(" https://artifactory.example/artifactory/ ")
	s.storage.serverSideCopy = true
	s.storage.plusWorkaround = true
	pool := &PackagePool{
		readManager:  &fakeJFrogManager{},
		writeManager: &fakeJFrogManager{},
		repository:   "pool-repo",
		prefix:       "pool-prefix",
		url:          normalizeArtifactoryURL("https://artifactory.example/artifactory"),
		openFn: func(string) (aptly.ReadSeekerCloser, error) {
			return nil, errors.New("copy path must not open the pool")
		},
	}

	err := s.storage.LinkFromPool("pool", "main/a+b", "pkg+test.deb", pool, "aa/bb/package+source.deb", aptly_utils.ChecksumInfo{}, false)
	c.Assert(err, IsNil)
	c.Assert(s.manager.copyParams, HasLen, 1)
	c.Assert(s.manager.copyParams[0].Pattern, Equals, path.Join("pool-repo", "pool-prefix", "aa/bb/package%2Bsource.deb"))
	c.Assert(s.manager.copyParams[0].Target, Equals, path.Join("repo", "prefix", "pool", "main/a%2Bb", "pkg%2Btest.deb"))
	c.Assert(s.manager.copyParams[0].Flat, Equals, true)
	c.Assert(s.manager.uploadParams, HasLen, 0)
}

func (s *PublishedStorageSuite) TestLinkFromDifferentJFrogServerFallsBackToUpload(c *C) {
	s.storage.url = normalizeArtifactoryURL("https://publish.example/artifactory")
	s.storage.serverSideCopy = true
	pool := &PackagePool{
		readManager:  &fakeJFrogManager{},
		writeManager: &fakeJFrogManager{},
		repository:   "pool-repo",
		prefix:       "pool-prefix",
		url:          normalizeArtifactoryURL("https://pool.example/artifactory"),
	}

	err := s.storage.LinkFromPool("pool", "main", "pkg.deb", pool, "aa/bb/package.deb", aptly_utils.ChecksumInfo{}, false)
	c.Assert(err, IsNil)
	c.Assert(s.manager.copyParams, HasLen, 0)
	c.Assert(s.manager.uploadParams, HasLen, 1)
}

func (s *PublishedStorageSuite) TestLinkFromJFrogPoolWithCopyDisabledFallsBackToUpload(c *C) {
	s.storage.url = normalizeArtifactoryURL("https://artifactory.example/artifactory")
	pool := &PackagePool{
		readManager:  &fakeJFrogManager{},
		writeManager: &fakeJFrogManager{},
		repository:   "pool-repo",
		prefix:       "pool-prefix",
		url:          normalizeArtifactoryURL("https://artifactory.example/artifactory"),
	}

	err := s.storage.LinkFromPool("pool", "main", "pkg.deb", pool, "aa/bb/package.deb", aptly_utils.ChecksumInfo{}, false)
	c.Assert(err, IsNil)
	c.Assert(s.manager.copyParams, HasLen, 0)
	c.Assert(s.manager.uploadParams, HasLen, 1)
}

func (s *PublishedStorageSuite) TestLinkFromMatchingJFrogPoolPropagatesCopyFailure(c *C) {
	s.storage.url = normalizeArtifactoryURL("https://artifactory.example/artifactory")
	s.storage.serverSideCopy = true
	s.manager.copySet = true
	s.manager.copyCount = 0
	s.manager.copyFailed = 1
	pool := &PackagePool{
		readManager:  &fakeJFrogManager{},
		writeManager: &fakeJFrogManager{},
		repository:   "pool-repo",
		prefix:       "pool-prefix",
		url:          normalizeArtifactoryURL("https://artifactory.example/artifactory"),
	}

	err := s.storage.LinkFromPool("pool", "main", "pkg.deb", pool, "aa/bb/package.deb", aptly_utils.ChecksumInfo{}, false)
	c.Assert(err, ErrorMatches, `.*pool-repo.*/aa/bb/package.deb.*repo.*/pool/main/pkg.deb.*`)
	c.Assert(s.manager.uploadParams, HasLen, 0)
}

func (s *PublishedStorageSuite) TestLinkFromMatchingJFrogPoolPropagatesCopyError(c *C) {
	s.storage.url = normalizeArtifactoryURL("https://artifactory.example/artifactory")
	s.storage.serverSideCopy = true
	s.manager.copyErr = errors.New("copy failed")
	pool := &PackagePool{
		readManager:  &fakeJFrogManager{},
		writeManager: &fakeJFrogManager{},
		repository:   "pool-repo",
		prefix:       "pool-prefix",
		url:          normalizeArtifactoryURL("https://artifactory.example/artifactory"),
	}

	err := s.storage.LinkFromPool("pool", "main", "pkg.deb", pool, "aa/bb/package.deb", aptly_utils.ChecksumInfo{}, false)
	c.Assert(err, NotNil)
	c.Assert(strings.Contains(err.Error(), path.Join("pool-repo", "pool-prefix", "aa/bb/package.deb")), Equals, true)
	c.Assert(strings.Contains(err.Error(), path.Join("repo", "prefix", "pool", "main", "pkg.deb")), Equals, true)
	c.Assert(s.manager.uploadParams, HasLen, 0)
}

func (s *PublishedStorageSuite) TestFilelist(c *C) {
	s.manager.searchReader = createReader(c, []jfrogutils.ResultItem{
		{Path: "repo/prefix/pool/main/a", Name: "a.deb", Actual_Md5: "m1"},
		{Path: "repo/prefix/pool/main/b", Name: "b.deb", Actual_Md5: "m2"},
	})

	list, err := s.storage.Filelist("pool/main")
	c.Assert(err, IsNil)
	c.Assert(list, DeepEquals, []string{"pool/main/a/a.deb", "pool/main/b/b.deb"})
	c.Assert(s.manager.searchParams[0].Pattern, Equals, path.Join("repo", "prefix", "pool/main", "*"))
}

func (s *PublishedStorageSuite) TestFilelistPlusWorkaroundAndSearchError(c *C) {
	s.storage.plusWorkaround = true
	s.manager.searchReader = createReader(c, []jfrogutils.ResultItem{
		{Path: "repo/prefix/pool/main", Name: "a%2Bb.deb", Actual_Md5: "m1"},
	})

	list, err := s.storage.Filelist("pool/main")
	c.Assert(err, IsNil)
	c.Assert(list, DeepEquals, []string{"pool/main/a+b.deb"})

	s.manager.searchErr = errors.New("search failed")
	_, err = s.storage.Filelist("pool/main")
	c.Assert(err, ErrorMatches, "search failed")
}

func (s *PublishedStorageSuite) TestRenameFile(c *C) {
	err := s.storage.RenameFile("old+name", "new+name")
	c.Assert(err, IsNil)
	c.Assert(len(s.manager.moveParams), Equals, 1)
	c.Assert(s.manager.moveParams[0].Pattern, Equals, path.Join("repo", "prefix", "old+name"))
	c.Assert(s.manager.moveParams[0].Target, Equals, path.Join("repo", "prefix", "new+name"))
	c.Assert(s.manager.moveParams[0].Flat, Equals, true)
}

func (s *PublishedStorageSuite) TestRenameFilePlusWorkaroundAndError(c *C) {
	s.storage.plusWorkaround = true
	s.manager.moveErr = errors.New("move failed")
	err := s.storage.RenameFile("old+name", "new+name")
	c.Assert(err, ErrorMatches, "move failed")
	c.Assert(s.manager.moveParams[0].Pattern, Equals, path.Join("repo", "prefix", "old%2Bname"))
	c.Assert(s.manager.moveParams[0].Target, Equals, path.Join("repo", "prefix", "new%2Bname"))
}

func (s *PublishedStorageSuite) TestSymLinkAndHardLink(c *C) {
	err := s.storage.SymLink("src+name", "dst+name")
	c.Assert(err, IsNil)
	c.Assert(len(s.manager.copyParams), Equals, 1)
	c.Assert(s.manager.copyParams[0].Pattern, Equals, path.Join("repo", "prefix", "src+name"))
	c.Assert(s.manager.copyParams[0].Target, Equals, path.Join("repo", "prefix", "dst+name"))
	c.Assert(s.manager.copyParams[0].Flat, Equals, true)
	targetProps := s.manager.copyParams[0].TargetProps.ToMap()
	c.Assert(targetProps["SymLink"], DeepEquals, []string{"src+name"})

	err = s.storage.HardLink("a", "b")
	c.Assert(err, IsNil)
	c.Assert(len(s.manager.copyParams), Equals, 2)
}

func (s *PublishedStorageSuite) TestSymLinkPlusWorkaroundAndError(c *C) {
	s.storage.plusWorkaround = true
	s.manager.copyErr = errors.New("copy failed")

	err := s.storage.SymLink("src+name", "dst+name")
	c.Assert(err, ErrorMatches, "copy failed")
	c.Assert(s.manager.copyParams[0].Pattern, Equals, path.Join("repo", "prefix", "src%2Bname"))
	c.Assert(s.manager.copyParams[0].Target, Equals, path.Join("repo", "prefix", "dst%2Bname"))
}

func (s *PublishedStorageSuite) TestSymLinkRejectsUnexpectedCopyCounts(c *C) {
	for _, test := range []struct {
		name   string
		copied int
		failed int
	}{
		{name: "partial copy", copied: 0, failed: 0},
		{name: "failed copy", copied: 0, failed: 1},
	} {
		c.Log(test.name)
		s.manager.copySet = true
		s.manager.copyCount = test.copied
		s.manager.copyFailed = test.failed

		err := s.storage.SymLink("src", "dst")
		c.Assert(err, ErrorMatches, `.*repo/prefix/src.*repo/prefix/dst.*`)
	}
}

func (s *PublishedStorageSuite) TestFileExists(c *C) {
	s.manager.searchReader = createReader(c, []jfrogutils.ResultItem{{Path: "repo/prefix/pool", Name: "x"}})
	ok, err := s.storage.FileExists("pool/x")
	c.Assert(err, IsNil)
	c.Assert(ok, Equals, true)
	c.Assert(s.manager.searchParams[0].Pattern, Equals, path.Join("repo", "prefix", "pool/x"))

	s.manager.searchReader = content.NewEmptyContentReader("results")
	ok, err = s.storage.FileExists("pool/y")
	c.Assert(err, IsNil)
	c.Assert(ok, Equals, false)
}

func (s *PublishedStorageSuite) TestFileExistsSearchErrorAndPlusWorkaround(c *C) {
	s.storage.plusWorkaround = true
	s.manager.searchErr = errors.New("search failed")
	ok, err := s.storage.FileExists("pool/a+b")
	c.Assert(ok, Equals, false)
	c.Assert(err, ErrorMatches, "search failed")
	c.Assert(s.manager.searchParams[0].Pattern, Equals, path.Join("repo", "prefix", "pool/a%2Bb"))
}

func (s *PublishedStorageSuite) TestReadLink(c *C) {
	s.manager.itemProps = &jfrogutils.ItemProperties{
		Properties: map[string][]string{
			"SymLink": {"src/file"},
		},
	}

	link, err := s.storage.ReadLink("path/to/link")
	c.Assert(err, IsNil)
	c.Assert(link, Equals, "src/file")
}

func (s *PublishedStorageSuite) TestReadLinkNoPropertyAndErrors(c *C) {
	s.manager.itemProps = &jfrogutils.ItemProperties{Properties: map[string][]string{"Other": {"value"}}}
	link, err := s.storage.ReadLink("path/to/link")
	c.Assert(err, IsNil)
	c.Assert(link, Equals, "")

	s.manager.itemPropsErr = errors.New("props failed")
	link, err = s.storage.ReadLink("path/to/link")
	c.Assert(err, IsNil)
	c.Assert(link, Equals, "")
}

func (s *PublishedStorageSuite) TestReadLinkPlusWorkaround(c *C) {
	s.storage.plusWorkaround = true
	s.manager.itemProps = &jfrogutils.ItemProperties{}
	_, _ = s.storage.ReadLink("a+b")
	// Ensure the method runs with plusWorkaround path conversion.
	c.Assert(s.manager.itemPropsErr, IsNil)
}

func (s *PublishedStorageSuite) TestCreatePublishedStorageConfig(c *C) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	withUserPassword, err := createJFrogConfig(server.URL, "user", "password", "", "", 0)
	c.Assert(err, IsNil)

	withUserPasswordDetails := withUserPassword.GetServiceDetails()
	c.Assert(withUserPasswordDetails, NotNil)
	c.Assert(withUserPasswordDetails.GetUser(), Equals, "user")

	withAPIKey, err := createJFrogConfig(server.URL, "", "", "api-123", "", 0)
	c.Assert(err, IsNil)

	withAPIKeyDetails := withAPIKey.GetServiceDetails()
	c.Assert(withAPIKeyDetails, NotNil)
	c.Assert(withAPIKeyDetails.GetApiKey(), Equals, "api-123")

	withAccessToken, err := createJFrogConfig(server.URL, "", "", "", "token", 0)
	c.Assert(err, IsNil)

	withAccessTokenDetails := withAccessToken.GetServiceDetails()
	c.Assert(withAccessTokenDetails, NotNil)
	c.Assert(withAccessTokenDetails.GetAccessToken(), Equals, "token")

	withUserPasswordFromEnv, err := createJFrogConfig(server.URL, "", "password", "", "", 0)
	c.Assert(err, IsNil)

	withUserPasswordFromEnvDetails := withUserPasswordFromEnv.GetServiceDetails()
	c.Assert(withUserPasswordFromEnvDetails, NotNil)
	c.Assert(withUserPasswordFromEnvDetails.GetUser(), Equals, "userfromenv")
}

func (s *PublishedStorageSuite) TestCreatePublishedStorageConfigRequiresCredentials(c *C) {
	_, err := createJFrogConfig("https://example.jfrog.local/artifactory", "", "", "", "", 0)
	c.Assert(err, ErrorMatches, "jfrog credentials are required: .*")
}

func (s *PublishedStorageSuite) TestCreateJFrogConfigUsesExplicitRetries(c *C) {
	serviceConfig, err := createJFrogConfig("https://example.jfrog.io/artifactory", "user", "password", "", "", 0)
	c.Assert(err, IsNil)
	c.Check(serviceConfig.GetHttpRetries(), Equals, 0)

	serviceConfig, err = createJFrogConfig("https://example.jfrog.io/artifactory", "user", "password", "", "", 4)
	c.Assert(err, IsNil)
	c.Check(serviceConfig.GetHttpRetries(), Equals, 4)
}

func (s *PublishedStorageSuite) TestNewPublishedStorageRaw(c *C) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	withUserPassword, err := NewPublishedStorageRaw("repo", server.URL, "user", "password", "", "", "prefix", true, false, 0)
	c.Assert(err, IsNil)
	c.Assert(withUserPassword, NotNil)
	c.Assert(withUserPassword.String(), Equals, "jfrog:repo:prefix")

	withAPIKey, err := NewPublishedStorageRaw("repo", server.URL, "", "", "api-key", "", "prefix", false, false, 0)
	c.Assert(err, IsNil)
	c.Assert(withAPIKey, NotNil)

	withToken, err := NewPublishedStorageRaw("repo", server.URL, "", "", "", "token", "prefix", false, false, 0)
	c.Assert(err, IsNil)
	c.Assert(withToken, NotNil)
}

func (s *PublishedStorageSuite) TestNewPublishedStorageRawManagerError(c *C) {
	// An SSH URL causes artifactory.New() to fail (no SSH key configured),
	// exercising the error return on lines 59-61.
	_, err := NewPublishedStorageRaw("repo", "ssh://example.local/artifactory", "", "", "", "token", "", false, false, 0)
	c.Assert(err, ErrorMatches, "error creating jfrog manager: .*")
}

func (s *PublishedStorageSuite) TestNewPublishedStorage(c *C) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	storage, err := NewPublishedStorage("test", aptly_utils.JFrogPublishRoot{
		Repository:     "repo",
		URL:            server.URL,
		AccessToken:    "token",
		Prefix:         "pref",
		PlusWorkaround: true,
	}, 0)
	c.Assert(err, IsNil)
	c.Assert(storage, NotNil)
	c.Assert(storage.String(), Equals, "jfrog:repo:pref")
}

var _ aptly.PublishedStorage = (*PublishedStorage)(nil)
