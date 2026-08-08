//go:build linux

package systemd

import (
	"errors"
	"os"
)

func requireRoot() error {
	if os.Geteuid() != 0 {
		return errors.New("this command must be run as root")
	}
	return nil
}
