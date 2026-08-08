//go:build !linux

package systemd

import "errors"

func requireRoot() error {
	return errors.New("systemd lifecycle commands are supported only on Linux")
}
