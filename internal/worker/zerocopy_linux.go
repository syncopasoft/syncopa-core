//go:build linux

package worker

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"syscall"
)

func tryZeroCopy(srcPath, dstPath string) (int64, string, bool, error) {
	src, err := os.Open(srcPath)
	if err != nil {
		return 0, "", false, err
	}
	defer src.Close()

	info, err := src.Stat()
	if err != nil {
		return 0, "", false, err
	}
	if !info.Mode().IsRegular() {
		return 0, "", false, nil
	}

	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return 0, "", false, err
	}
	defer dst.Close()

	var written int64
	var offset int64
	size := info.Size()
	if size > 0 {
		const maxChunk = 1 << 30
		for written < size {
			remaining := size - written
			if remaining > maxChunk {
				remaining = maxChunk
			}
			n, err := syscall.Sendfile(int(dst.Fd()), int(src.Fd()), &offset, int(remaining))
			if err != nil {
				switch err {
				case syscall.ENOSYS, syscall.EINVAL, syscall.EOPNOTSUPP, syscall.EPERM:
					_ = os.Remove(dstPath)
					return 0, "", false, nil
				case syscall.EINTR, syscall.EAGAIN:
					continue
				default:
					return written, "", true, err
				}
			}
			if n == 0 {
				break
			}
			written += int64(n)
		}
		if written < size {
			return written, "", true, io.ErrShortWrite
		}
	}

	if err := dst.Sync(); err != nil {
		return written, "", true, err
	}
	if err := os.Chmod(dstPath, info.Mode().Perm()); err != nil {
		return written, "", true, err
	}
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return written, "", true, err
	}
	hasher := sha256.New()
	if _, err := io.Copy(hasher, src); err != nil {
		return written, "", true, err
	}
	hash := hex.EncodeToString(hasher.Sum(nil))
	return written, hash, true, nil
}
