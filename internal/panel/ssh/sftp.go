package ssh

import (
	"os"

	"github.com/pkg/sftp"
	xssh "golang.org/x/crypto/ssh"
)

// putSFTP writes a file on a server with SFTP.
//
// The file is written next to its place and moved there, so a reader never
// sees half of it. A server without an SFTP subsystem says so, and the
// caller writes the file with the shell instead.
func putSFTP(client *xssh.Client, data []byte, path string, mode os.FileMode) error {
	c, err := sftp.NewClient(client)
	if err != nil {
		return err
	}
	defer c.Close()

	tmp := path + ".bt-tmp"
	file, err := c.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := c.Chmod(tmp, mode); err != nil {
		return err
	}
	return c.PosixRename(tmp, path)
}
