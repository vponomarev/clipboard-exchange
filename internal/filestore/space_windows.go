//go:build windows

package filestore

import "golang.org/x/sys/windows"

func (s *Store) DiskSpace() (available, total uint64, err error) {
	path, err := windows.UTF16PtrFromString(s.root)
	if err != nil {
		return 0, 0, err
	}
	var freeTotal uint64
	if err := windows.GetDiskFreeSpaceEx(path, &available, &total, &freeTotal); err != nil {
		return 0, 0, err
	}
	return available, total, nil
}
