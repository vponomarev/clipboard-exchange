//go:build !windows

package filestore

import "golang.org/x/sys/unix"

func (s *Store) DiskSpace() (available, total uint64, err error) {
	var stat unix.Statfs_t
	if err = unix.Statfs(s.root, &stat); err != nil {
		return 0, 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), stat.Blocks * uint64(stat.Bsize), nil
}
