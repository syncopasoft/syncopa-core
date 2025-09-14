package scanner

import (
	"io/fs"
	"path/filepath"

	"migratool/internal/task"
)

// Scan walks the source directory and emits copy tasks.
func Scan(src, dst string, tasks chan<- task.Task) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		tasks <- task.Task{Src: path, Dst: filepath.Join(dst, rel)}
		return nil
	})
}
