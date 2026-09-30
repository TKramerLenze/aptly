package context

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aptly-dev/aptly/files"
	"github.com/aptly-dev/aptly/utils"
	"github.com/smira/flag"

	. "gopkg.in/check.v1"
)

func Test(t *testing.T) { TestingT(t) }

type fatalErrorPanicChecker struct {
	*CheckerInfo
}

var FatalErrorPanicMatches Checker = &fatalErrorPanicChecker{
	&CheckerInfo{Name: "FatalErrorPanics", Params: []string{"function", "expected"}},
}

func (checker *fatalErrorPanicChecker) Check(params []interface{}, names []string) (result bool, errmsg string) {
	f := reflect.ValueOf(params[0])
	if f.Kind() != reflect.Func || f.Type().NumIn() != 0 {
		return false, "Function must take zero arguments"
	}
	defer func() {
		if errmsg != "" {
			return
		}

		obtained := recover()

		names[0] = "panic"

		var ok bool

		var e1 *FatalError
		if e1, ok = obtained.(*FatalError); ok {
			params[0] = e1
		} else {
			errmsg = "Panic value is not FatalError"
			return
		}

		var e2 *FatalError
		if e2, ok = params[1].(*FatalError); ok {
			params[1] = e2
		} else {
			errmsg = "Expected value is not FatalError"
			return
		}

		if *e1 == *e2 {
			result = true
		} else {
			result = false
			errmsg = "Not equal"
		}
	}()
	f.Call(nil)
	return false, "Function has not panicked"
}

type AptlyContextSuite struct {
	context *AptlyContext
}

var _ = Suite(&AptlyContextSuite{})

func (s *AptlyContextSuite) SetUpTest(c *C) {
	flags := flag.NewFlagSet("fakeFlags", flag.ContinueOnError)
	flags.String("config", "", "")
	context, err := NewContext(flags)
	c.Assert(err, IsNil)
	s.context = context
}

func (s *AptlyContextSuite) TestGetPublishedStorageBadFS(c *C) {
	// https://github.com/aptly-dev/aptly/issues/711
	// https://github.com/aptly-dev/aptly/issues/1477
	// GetPublishedStorage must return an error (not panic) when the
	// requested storage is not configured.
	_, err := s.context.GetPublishedStorage("filesystem:fuji")
	c.Assert(err, NotNil)
}

func (s *AptlyContextSuite) TestGetPublishedStorageJFrogConfigured(c *C) {
	prevConfig := utils.Config
	defer func() { utils.Config = prevConfig }()

	s.context.configLoaded = true
	utils.Config.RootDir = c.MkDir()
	utils.Config.JFrogPublishRoots = map[string]utils.JFrogPublishRoot{
		"test": {
			Repository:  "aptly-repo",
			URL:         "https://example.jfrog.local/artifactory",
			AccessToken: "token",
			Prefix:      "public",
		},
	}

	storage, err := s.context.GetPublishedStorage("jfrog:test")
	c.Assert(err, IsNil)
	c.Assert(storage, NotNil)
	c.Assert(fmt.Sprintf("%v", storage), Equals, "jfrog:aptly-repo:public")

	// Ensure we get the cached object on repeated lookups.
	storageAgain, err := s.context.GetPublishedStorage("jfrog:test")
	c.Assert(err, IsNil)
	c.Assert(storageAgain, Equals, storage)
}

func (s *AptlyContextSuite) TestGetPublishedStorageJFrogMissing(c *C) {
	prevConfig := utils.Config
	defer func() { utils.Config = prevConfig }()

	s.context.configLoaded = true
	utils.Config.JFrogPublishRoots = map[string]utils.JFrogPublishRoot{}

	_, err := s.context.GetPublishedStorage("jfrog:missing")
	c.Assert(err, NotNil)
	c.Check(err.Error(), Equals, "published JFrog storage missing not configured")
}

func (s *AptlyContextSuite) TestGetPublishedStorageJFrogInitError(c *C) {
	prevConfig := utils.Config
	defer func() { utils.Config = prevConfig }()

	s.context.configLoaded = true
	utils.Config.JFrogPublishRoots = map[string]utils.JFrogPublishRoot{
		"broken": {
			Repository: "aptly-repo",
			URL:        "ssh://example.local/artifactory",
		},
	}

	_, err := s.context.GetPublishedStorage("jfrog:broken")
	c.Assert(err, NotNil)
	c.Check(err.Error(), Matches, `error creating jfrog manager: .*`)
}

func TestPackagePoolJFrogConfigured(t *testing.T) {
	prevConfig := utils.Config
	defer func() { utils.Config = prevConfig }()

	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "aptly-jfrog.yaml")
	err := os.WriteFile(configPath, []byte(`root_dir: /tmp/aptly
packagepool_storage:
    type: jfrog
    repository: team-debian-aptly-backend
    url: http://127.0.0.1:1/artifactory
    access_token: test-token
    prefix: aptly-pool
`), 0o600)
	if err != nil {
		t.Fatalf("failed to write config fixture: %v", err)
	}

	flags := flag.NewFlagSet("fakeFlags", flag.ContinueOnError)
	flags.String("config", configPath, "")
	context, err := NewContext(flags)
	if err != nil {
		t.Fatalf("failed to create aptly context: %v", err)
	}

	pool := context.PackagePool()
	if pool == nil {
		t.Fatal("expected non-nil package pool")
	}

	_, isFilesPool := pool.(*files.PackagePool)
	if isFilesPool {
		t.Fatalf("expected jfrog package pool, got files package pool (%T)", pool)
	}
}
