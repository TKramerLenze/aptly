package jfrog

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aptly-dev/aptly/aptly"
	"github.com/aptly-dev/aptly/utils"
	"github.com/jfrog/jfrog-client-go/artifactory"
	"github.com/jfrog/jfrog-client-go/artifactory/services"
	jfrogutils "github.com/jfrog/jfrog-client-go/artifactory/services/utils"
)

// PackagePool is a JFrog-backed package pool implementation.
type PackagePool struct {
	manager    artifactory.ArtifactoryServicesManager
	repository string
	prefix     string
	openFn     func(relPath string) (aptly.ReadSeekerCloser, error)
}

var _ aptly.PackagePool = (*PackagePool)(nil)

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

func (pool *PackagePool) ensureChecksums(poolPath string, checksumStorage aptly.ChecksumStorage, remotePresent bool) (*utils.ChecksumInfo, error) {
	targetChecksums, err := checksumStorage.Get(poolPath)
	if err != nil {
		return nil, fmt.Errorf("error loading checksums for %q: %w", poolPath, err)
	}

	if targetChecksums != nil {
		return targetChecksums, nil
	}
	if !remotePresent {
		results, err := pool.search(pool.qualifiedObjectKey(poolPath), false)
		if err != nil {
			return nil, fmt.Errorf("error searching jfrog package pool key %q: %w", poolPath, err)
		}

		if len(results) == 0 {
			return nil, nil
		}

		if len(results) != 1 {
			return nil, fmt.Errorf("expected exactly one search result for %q, got %d", poolPath, len(results))
		}
	}

	reader, err := pool.openChecksumReader(poolPath)
	if err != nil {
		return nil, fmt.Errorf("error opening jfrog package pool key %q for checksums: %w", poolPath, err)
	}

	computedChecksums, checksumErr := utils.ChecksumsForReader(reader)
	closeErr := reader.Close()
	if checksumErr != nil {
		return nil, fmt.Errorf("error checksumming jfrog package pool key %q: %w", poolPath, checksumErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("error closing jfrog package pool reader for %q: %w", poolPath, closeErr)
	}

	targetChecksums = &computedChecksums
	if err := checksumStorage.Update(poolPath, targetChecksums); err != nil {
		return nil, fmt.Errorf("error storing checksums for %q: %w", poolPath, err)
	}

	return targetChecksums, nil
}

func (pool *PackagePool) openChecksumReader(relPath string) (aptly.ReadSeekerCloser, error) {
	if pool.openFn != nil {
		return pool.openFn(relPath)
	}

	return pool.Open(relPath)
}

func (pool *PackagePool) Import(srcPath, basename string, checksums *utils.ChecksumInfo, _ bool, checksumStorage aptly.ChecksumStorage) (string, error) {
	if checksums.MD5 == "" || checksums.SHA256 == "" || checksums.SHA512 == "" {
		var err error
		*checksums, err = utils.ChecksumsForFile(srcPath)
		if err != nil {
			return "", err
		}
	}

	poolPath, err := pool.buildPoolPath(basename, checksums)
	if err != nil {
		return "", err
	}

	remoteResult, err := pool.findExactObject(poolPath)
	if err != nil {
		return "", fmt.Errorf("error searching jfrog package pool key %q: %w", poolPath, err)
	}

	if remoteResult != nil {
		targetChecksums, ensureErr := pool.ensureChecksums(poolPath, checksumStorage, true)
		if ensureErr != nil {
			return "", ensureErr
		}
		if targetChecksums != nil {
			*checksums = *targetChecksums
			return poolPath, nil
		}
	}

	uploadParams := services.NewUploadParams()
	uploadParams.Pattern = srcPath
	uploadParams.Target = pool.qualifiedObjectKey(poolPath)
	uploadParams.Flat = true

	uploaded, failed, err := pool.manager.UploadFiles(artifactory.UploadServiceOptions{}, uploadParams)
	if err != nil {
		return "", fmt.Errorf("error uploading %q to jfrog package pool key %q: %w", srcPath, poolPath, err)
	}

	if uploaded != 1 || failed != 0 {
		return "", fmt.Errorf(
			"error uploading %q to jfrog package pool key %q: expected uploaded=1 and failed=0, got uploaded=%d failed=%d",
			srcPath,
			poolPath,
			uploaded,
			failed,
		)
	}

	if !checksums.Complete() {
		*checksums, err = utils.ChecksumsForFile(srcPath)
		if err != nil {
			return "", err
		}
	}

	if err := checksumStorage.Update(poolPath, checksums); err != nil {
		return "", fmt.Errorf("error storing checksums for %q: %w", poolPath, err)
	}

	return poolPath, nil
}

func (pool *PackagePool) Verify(poolPath, basename string, checksums *utils.ChecksumInfo, checksumStorage aptly.ChecksumStorage) (string, bool, error) {
	if poolPath == "" {
		if checksums.SHA256 == "" {
			return "", false, nil
		}

		var err error
		poolPath, err = pool.buildPoolPath(basename, checksums)
		if err != nil {
			return "", false, err
		}
	}

	remoteResult, err := pool.findExactObject(poolPath)
	if err != nil {
		return "", false, fmt.Errorf("error checking size for jfrog package pool key %q: %w", poolPath, err)
	}
	if remoteResult == nil {
		return "", false, nil
	}
	if remoteResult.Size != checksums.Size {
		return "", false, nil
	}

	targetChecksums, err := pool.ensureChecksums(poolPath, checksumStorage, true)
	if err != nil {
		return "", false, err
	}
	if targetChecksums == nil {
		return "", false, nil
	}

	if checksums.MD5 != "" && targetChecksums.MD5 != checksums.MD5 ||
		checksums.SHA256 != "" && targetChecksums.SHA256 != checksums.SHA256 {
		return "", false, nil
	}

	*checksums = *targetChecksums
	return poolPath, true, nil
}

func (pool *PackagePool) LegacyPath(_ string, _ *utils.ChecksumInfo) (string, error) {
	return "", errors.New("JFrog package pool does not support legacy paths")
}

func (pool *PackagePool) Size(relPath string) (int64, error) {
	results, err := pool.search(pool.qualifiedObjectKey(relPath), false)
	if err != nil {
		return 0, err
	}

	if len(results) != 1 {
		return 0, fmt.Errorf("expected exactly one search result for %q, got %d", relPath, len(results))
	}

	return results[0].Size, nil
}

func (pool *PackagePool) Open(relPath string) (aptly.ReadSeekerCloser, error) {
	tempDir, err := os.MkdirTemp("", "aptly-jfrog-pool-*")
	if err != nil {
		return nil, fmt.Errorf("error creating temp dir for %q: %w", relPath, err)
	}

	cleanup := func() {
		_ = os.RemoveAll(tempDir)
	}

	download := services.NewDownloadParams()
	download.Pattern = pool.qualifiedObjectKey(relPath)
	download.Target = tempDir + string(os.PathSeparator)
	download.Flat = true

	downloaded, failed, err := pool.manager.DownloadFiles(download)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("error downloading %q from jfrog package pool: %w", relPath, err)
	}
	if downloaded == 0 && failed == 0 {
		cleanup()
		return nil, fmt.Errorf("error downloading %q from jfrog package pool: no files were downloaded", relPath)
	}
	if failed > 0 {
		cleanup()
		return nil, fmt.Errorf("error downloading %q from jfrog package pool: %d file(s) failed", relPath, failed)
	}

	file, err := os.Open(filepath.Join(tempDir, path.Base(relPath)))
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("error opening downloaded package %q: %w", relPath, err)
	}

	return &tempPackageReadSeeker{
		File:    file,
		tempDir: tempDir,
	}, nil
}

func (pool *PackagePool) FilepathList(progress aptly.Progress) ([]string, error) {
	if progress != nil {
		progress.InitBar(0, false, aptly.BarGeneralBuildFileList)
		defer progress.ShutdownBar()
	}

	results, err := pool.search(path.Join(pool.repository, pool.prefix, "*"), true)
	if err != nil {
		return nil, err
	}

	paths := make([]string, 0, len(results))
	for _, result := range results {
		relPath, err := pool.relativeObjectKey(result)
		if err != nil {
			return nil, err
		}
		paths = append(paths, relPath)
	}

	sort.Strings(paths)
	return paths, nil
}

func (pool *PackagePool) Remove(relPath string) (int64, error) {
	size, err := pool.Size(relPath)
	if err != nil {
		return 0, err
	}

	deleteParams := services.NewDeleteParams()
	deleteParams.SetPattern(pool.qualifiedObjectKey(relPath))

	toDelete, err := pool.manager.GetPathsToDelete(deleteParams)
	if err != nil {
		return 0, err
	}
	defer func() { _ = toDelete.Close() }()

	if _, err := pool.manager.DeleteFiles(toDelete); err != nil {
		return 0, err
	}

	return size, nil
}

func (pool *PackagePool) buildPoolPath(filename string, checksums *utils.ChecksumInfo) (string, error) {
	basename := path.Base(filename)
	if basename == "." || basename == "/" {
		return "", fmt.Errorf("filename %s is invalid", filename)
	}

	hash := checksums.SHA256
	if len(hash) < 32 {
		return "", fmt.Errorf("unable to compute pool location for filename %v, SHA256 is missing", basename)
	}

	return path.Join(hash[0:2], hash[2:4], hash[4:32]+"_"+basename), nil
}

func (pool *PackagePool) qualifiedObjectKey(relPath string) string {
	return path.Join(pool.repository, pool.prefix, relPath)
}

func (pool *PackagePool) findExactObject(relPath string) (*jfrogutils.ResultItem, error) {
	results, err := pool.search(pool.qualifiedObjectKey(relPath), false)
	if err != nil {
		return nil, err
	}

	if len(results) == 0 {
		return nil, nil
	}

	if len(results) != 1 {
		return nil, fmt.Errorf("expected exactly one search result for %q, got %d", relPath, len(results))
	}

	return &results[0], nil
}

func (pool *PackagePool) search(pattern string, recursive bool) ([]jfrogutils.ResultItem, error) {
	params := services.NewSearchParams()
	params.Pattern = pattern
	params.Recursive = recursive

	reader, err := pool.manager.SearchFiles(params)
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close() }()

	results := []jfrogutils.ResultItem{}
	for {
		item := new(jfrogutils.ResultItem)
		nextErr := reader.NextRecord(item)
		if nextErr != nil {
			if errors.Is(nextErr, io.EOF) {
				break
			}
			return nil, fmt.Errorf("error reading jfrog search results: %w", nextErr)
		}
		results = append(results, *item)
	}

	if readErr := reader.GetError(); readErr != nil {
		return nil, fmt.Errorf("error reading jfrog search results: %w", readErr)
	}

	return results, nil
}

func (pool *PackagePool) relativeObjectKey(result jfrogutils.ResultItem) (string, error) {
	fullPath := path.Join(result.Path, result.Name)
	if result.Repo != "" {
		fullPath = path.Join(result.Repo, result.Path, result.Name)
	}

	prefix := path.Join(pool.repository, pool.prefix)
	if fullPath == prefix {
		return "", nil
	}

	prefixWithSlash := prefix + "/"
	if !strings.HasPrefix(fullPath, prefixWithSlash) {
		return "", fmt.Errorf("search result path %q is outside configured pool prefix %q", fullPath, prefix)
	}

	return strings.TrimPrefix(fullPath, prefixWithSlash), nil
}

type tempPackageReadSeeker struct {
	*os.File
	tempDir string
}

func (reader *tempPackageReadSeeker) Close() error {
	return errors.Join(reader.File.Close(), os.RemoveAll(reader.tempDir))
}
