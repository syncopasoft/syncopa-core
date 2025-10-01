//go:build !linux

package worker

func tryZeroCopy(srcPath, dstPath string) (int64, string, bool, error) {
	return 0, "", false, nil
}
