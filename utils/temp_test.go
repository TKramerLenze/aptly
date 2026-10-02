package utils

import (
	"os"
	"path/filepath"
	"strings"

	. "gopkg.in/check.v1"
)

func (s *UtilsSuite) TestCreateTempPathAcceptsNestedFilename(c *C) {
	tempPath, err := CreateTempPath("./netboot/debian-installer/amd64/grub/grub.cfg")
	c.Assert(err, IsNil)
	defer func() { _ = os.Remove(tempPath) }()

	_, err = os.Stat(tempPath)
	c.Assert(err, IsNil)
	c.Assert(filepath.Dir(tempPath), Equals, os.TempDir())
	c.Assert(strings.HasPrefix(filepath.Base(tempPath), "grub.cfg"), Equals, true)
}
