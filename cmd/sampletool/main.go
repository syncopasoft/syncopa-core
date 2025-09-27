package main

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	mrand "math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var (
	adjectives = []string{"bright", "calm", "daring", "elegant", "fresh", "gentle", "lively", "mellow", "quick", "vivid"}
	nouns      = []string{"analysis", "brief", "budget", "concept", "plan", "proposal", "report", "schedule", "summary", "update"}
	extensions = []string{"docx", "xlsx", "pptx", "txt", "md", "csv", "pdf"}
)

func main() {
	dirFlag := flag.String("dir", "./sample-data", "output directory for generated files")
	countFlag := flag.Int("count", 10, "number of files to create")
	sizesFlag := flag.String("sizes", "16KB,64KB,256KB", "comma separated list of target file sizes")
	maxBytesFlag := flag.String("max-bytes", "10MB", "maximum total bytes to generate (0 for no limit)")
	flag.Parse()

	if *countFlag <= 0 {
		fmt.Fprintln(os.Stderr, "count must be greater than zero")
		os.Exit(1)
	}

	sizes, err := parseSizes(*sizesFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid sizes: %v\n", err)
		os.Exit(1)
	}
	if len(sizes) == 0 {
		fmt.Fprintln(os.Stderr, "at least one size must be provided")
		os.Exit(1)
	}

	maxBytes, err := parseByteSize(*maxBytesFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid max-bytes: %v\n", err)
		os.Exit(1)
	}
	if maxBytes < 0 {
		fmt.Fprintln(os.Stderr, "max-bytes cannot be negative")
		os.Exit(1)
	}
	if err := os.MkdirAll(*dirFlag, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "failed to create directory: %v\n", err)
		os.Exit(1)
	}

	seedMathRand()

	var (
		totalBytes   int64
		createdCount int
	)
	for i := 0; i < *countFlag; i++ {
		remaining := maxBytes
		if maxBytes > 0 {
			remaining -= totalBytes
			if remaining <= 0 {
				fmt.Printf("Reached maximum total bytes (%s). Stopping.\n", humanReadable(maxBytes))
				break
			}
		}
		size := chooseSize(sizes, remaining)
		if size == 0 {
			fmt.Println("No remaining capacity to create another file within size constraints. Stopping.")
			break
		}

		name := uniqueFileName(*dirFlag)
		ext := extensions[mrand.Intn(len(extensions))]
		filename := fmt.Sprintf("%s.%s", name, ext)
		path := filepath.Join(*dirFlag, filename)

		if err := createFile(path, size); err != nil {
			fmt.Fprintf(os.Stderr, "failed to create %s: %v\n", path, err)
			os.Exit(1)
		}

		if err := randomizeTimes(path, createdCount); err != nil {
			fmt.Fprintf(os.Stderr, "failed to adjust timestamps for %s: %v\n", path, err)
			os.Exit(1)
		}

		totalBytes += size
		createdCount++
		fmt.Printf("Created %s (%s)\n", path, humanReadable(size))
	}

	fmt.Printf("Generated %d file(s) totalling %s.\n", createdCount, humanReadable(totalBytes))
}

func parseSizes(input string) ([]int64, error) {
	if strings.TrimSpace(input) == "" {
		return nil, errors.New("empty sizes string")
	}
	parts := strings.Split(input, ",")
	sizes := make([]int64, 0, len(parts))
	for _, part := range parts {
		size, err := parseByteSize(strings.TrimSpace(part))
		if err != nil {
			return nil, err
		}
		if size <= 0 {
			return nil, errors.New("sizes must be positive")
		}
		sizes = append(sizes, size)
	}
	return sizes, nil
}

func parseByteSize(input string) (int64, error) {
	s := strings.TrimSpace(strings.ToUpper(input))
	if s == "" {
		return 0, errors.New("empty size")
	}
	units := []struct {
		suffix     string
		multiplier int64
	}{
		{"GB", 1024 * 1024 * 1024},
		{"G", 1024 * 1024 * 1024},
		{"MB", 1024 * 1024},
		{"M", 1024 * 1024},
		{"KB", 1024},
		{"K", 1024},
		{"B", 1},
		{"", 1},
	}
	for _, unit := range units {
		if strings.HasSuffix(s, unit.suffix) {
			valuePart := strings.TrimSpace(strings.TrimSuffix(s, unit.suffix))
			if valuePart == "" {
				return 0, errors.New("missing value for size")
			}
			value, err := parseInt(valuePart)
			if err != nil {
				return 0, err
			}
			return value * unit.multiplier, nil
		}
	}
	return 0, fmt.Errorf("unrecognised size %q", input)
}

func parseInt(value string) (int64, error) {
	var n int64
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("invalid digit %q", r)
		}
		n = n*10 + int64(r-'0')
	}
	return n, nil
}

func chooseSize(sizes []int64, remaining int64) int64 {
	if remaining <= 0 {
		return sizes[mrand.Intn(len(sizes))]
	}
	candidates := make([]int64, 0, len(sizes))
	for _, size := range sizes {
		if size <= remaining {
			candidates = append(candidates, size)
		}
	}
	if len(candidates) == 0 {
		return 0
	}
	return candidates[mrand.Intn(len(candidates))]
}

func uniqueFileName(dir string) string {
	for {
		name := fmt.Sprintf("%s-%s", adjectives[mrand.Intn(len(adjectives))], nouns[mrand.Intn(len(nouns))])
		if mrand.Intn(2) == 0 {
			name = fmt.Sprintf("%s-%d", name, mrand.Intn(9000)+1000)
		}
		pattern := filepath.Join(dir, name+".*")
		matches, _ := filepath.Glob(pattern)
		if len(matches) == 0 {
			return name
		}
	}
}

func createFile(path string, size int64) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	chunk := []byte("Sample office document generated by sampletool.\n")
	written := int64(0)
	for written < size {
		remaining := size - written
		if remaining < int64(len(chunk)) {
			_, err = f.Write(chunk[:remaining])
		} else {
			_, err = f.Write(chunk)
		}
		if err != nil {
			return err
		}
		if remaining < int64(len(chunk)) {
			written += remaining
		} else {
			written += int64(len(chunk))
		}
	}
	return f.Sync()
}

func randomizeTimes(path string, fileIndex int) error {
	now := time.Now()
	maxBack := 365 * 24 * time.Hour
	baseOffset := time.Duration(mrand.Int63n(int64(maxBack)))
	uniqueOffset := time.Duration(fileIndex+1) * time.Second
	modTime := now.Add(-(baseOffset + uniqueOffset))
	accessJitter := time.Duration(mrand.Intn(6*60*60)+1) * time.Second // within the last 6 hours relative to modTime
	accessTime := modTime.Add(accessJitter)
	if accessTime.After(now) {
		accessTime = modTime.Add(-time.Second)
	}
	return os.Chtimes(path, accessTime, modTime)
}

func humanReadable(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(size)/float64(div), "KMGTPE"[exp])
}

func seedMathRand() {
	var seed int64
	if err := binary.Read(rand.Reader, binary.BigEndian, &seed); err != nil {
		seed = time.Now().UnixNano()
	}
	mrand.Seed(seed)
}
