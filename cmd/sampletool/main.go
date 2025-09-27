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
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
)

var (
	adjectives        = []string{"bright", "calm", "daring", "elegant", "fresh", "gentle", "lively", "mellow", "quick", "vivid"}
	nouns             = []string{"analysis", "brief", "budget", "concept", "plan", "proposal", "report", "schedule", "summary", "update"}
	extensions        = []string{"docx", "xlsx", "pptx", "txt", "md", "csv", "pdf"}
	teamMembers       = []string{"Alex Morgan", "Jordan Lee", "Taylor Chen", "Priya Patel", "Diego Alvarez", "Morgan Blake", "Jules Carter", "Nina Rossi"}
	officeDepartments = map[string][]string{
		"Finance":         {"Budgets", "Audits", "Invoices"},
		"Human Resources": {"Policies", "Recruiting", "Benefits"},
		"Engineering":     {"Roadmaps", "Designs", "Operations"},
		"Marketing":       {"Campaigns", "Content", "Analytics"},
		"Sales":           {"Reports", "Forecasts", "Enablement"},
		"Product":         {"Research", "Planning", "Releases"},
	}
)

type officeLocation struct {
	Department string
	Category   string
	Path       string
}

type fileTask struct {
	path    string
	size    int64
	index   int
	header  []byte
	filler  []byte
	message string
}

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

	locations, err := buildOfficeStructure(*dirFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to prepare office folders: %v\n", err)
		os.Exit(1)
	}
	if len(locations) == 0 {
		fmt.Fprintln(os.Stderr, "no office locations available to create files")
		os.Exit(1)
	}

	seedMathRand()

	var (
		totalBytes int64
		tasks      []fileTask
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

		location := locations[mrand.Intn(len(locations))]
		ext := extensions[mrand.Intn(len(extensions))]
		name := uniqueFileName(location.Path, location.Department, location.Category)
		filename := fmt.Sprintf("%s.%s", name, ext)
		path := filepath.Join(location.Path, filename)

		header, filler, message := buildContentProfile(ext, location, name)
		tasks = append(tasks, fileTask{
			path:    path,
			size:    size,
			index:   len(tasks),
			header:  header,
			filler:  filler,
			message: message,
		})
		totalBytes += size
	}

	if len(tasks) == 0 {
		fmt.Println("No files generated due to constraints.")
		return
	}

	if err := executeTasksConcurrently(tasks); err != nil {
		fmt.Fprintf(os.Stderr, "failed to create files: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Generated %d file(s) totalling %s.\n", len(tasks), humanReadable(totalBytes))
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

func buildOfficeStructure(root string) ([]officeLocation, error) {
	locations := make([]officeLocation, 0)
	for department, categories := range officeDepartments {
		deptPath := filepath.Join(root, department)
		if err := os.MkdirAll(deptPath, 0o755); err != nil {
			return nil, err
		}
		for _, category := range categories {
			categoryPath := filepath.Join(deptPath, category)
			if err := os.MkdirAll(categoryPath, 0o755); err != nil {
				return nil, err
			}
			locations = append(locations, officeLocation{
				Department: department,
				Category:   category,
				Path:       categoryPath,
			})
		}
	}
	return locations, nil
}

func executeTasksConcurrently(tasks []fileTask) error {
	workerCount := runtime.NumCPU()
	if workerCount > len(tasks) {
		workerCount = len(tasks)
	}
	if workerCount < 1 {
		workerCount = 1
	}

	taskCh := make(chan fileTask)
	var wg sync.WaitGroup
	var once sync.Once
	var execErr error
	var hasErr atomic.Bool

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range taskCh {
				if hasErr.Load() {
					continue
				}
				if err := createFile(task.path, task.size, task.header, task.filler); err != nil {
					hasErr.Store(true)
					once.Do(func() {
						execErr = fmt.Errorf("failed to create %s: %w", task.path, err)
					})
					continue
				}
				if err := randomizeTimes(task.path, task.index); err != nil {
					hasErr.Store(true)
					once.Do(func() {
						execErr = fmt.Errorf("failed to adjust timestamps for %s: %w", task.path, err)
					})
					continue
				}
				fmt.Printf("Created %s (%s)%s\n", task.path, humanReadable(task.size), task.message)
			}
		}()
	}

	go func() {
		for _, task := range tasks {
			taskCh <- task
		}
		close(taskCh)
	}()

	wg.Wait()

	if execErr != nil {
		return execErr
	}
	if hasErr.Load() {
		return errors.New("task execution halted due to previous errors")
	}
	return nil
}

func uniqueFileName(dir, department, category string) string {
	prefixParts := []string{slugify(department), slugify(category)}
	basePrefix := strings.Trim(strings.Join(prefixParts, "-"), "-")
	if basePrefix != "" {
		basePrefix += "-"
	}
	for {
		name := fmt.Sprintf("%s%s-%s", basePrefix, adjectives[mrand.Intn(len(adjectives))], nouns[mrand.Intn(len(nouns))])
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

func createFile(path string, size int64, header, filler []byte) error {
	if size <= 0 {
		return errors.New("size must be positive")
	}
	if len(filler) == 0 {
		filler = []byte(" ")
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	var written int64
	if err := writeSection(f, header, size, &written); err != nil {
		return err
	}
	for written < size {
		if err := writeSection(f, filler, size, &written); err != nil {
			return err
		}
	}
	return f.Sync()
}

func writeSection(f *os.File, data []byte, target int64, written *int64) error {
	if len(data) == 0 || *written >= target {
		return nil
	}
	remaining := target - *written
	if remaining <= 0 {
		return nil
	}
	if int64(len(data)) > remaining {
		data = data[:remaining]
	}
	n, err := f.Write(data)
	if err != nil {
		return err
	}
	*written += int64(n)
	return nil
}

func buildContentProfile(ext string, location officeLocation, name string) ([]byte, []byte, string) {
	owner := teamMembers[mrand.Intn(len(teamMembers))]
	updated := time.Now().Add(-time.Duration(mrand.Intn(120)) * 24 * time.Hour).Format("2006-01-02")
	title := formatTitle(name)

	var header, filler []byte
	switch strings.ToLower(ext) {
	case "csv":
		header = []byte("Title,Department,Category,Owner,LastUpdated,Summary\n")
		filler = []byte(fmt.Sprintf("%s,%s,%s,%s,%s,%s\n", title, location.Department, location.Category, owner, updated, "Status update recorded"))
	case "md":
		header = []byte(fmt.Sprintf("# %s\n\n- Department: %s\n- Category: %s\n- Owner: %s\n- Last Updated: %s\n\n", title, location.Department, location.Category, owner, updated))
		filler = []byte(fmt.Sprintf("## Highlights\n\n- Key initiatives for the %s team.\n- Upcoming reviews scheduled.\n- Risks and mitigations documented.\n\n", location.Department))
	case "txt":
		header = []byte(fmt.Sprintf("Title: %s\nDepartment: %s\nCategory: %s\nOwner: %s\nLast Updated: %s\n\n", title, location.Department, location.Category, owner, updated))
		filler = []byte("Action Items:\n- Confirm deliverables.\n- Share updates with stakeholders.\n- Archive supporting documents.\n\n")
	case "pdf":
		header = []byte(fmt.Sprintf("%%PDF-1.4\n%%âãÏÓ\n%% Sampletool generated overview for %s / %s\n", location.Department, location.Category))
		filler = []byte("This simulated PDF content represents office documentation for workflow testing.\n")
	case "docx":
		header = []byte(fmt.Sprintf("Office Document\nTitle: %s\nDepartment: %s\nCategory: %s\nOwner: %s\nLast Updated: %s\n\n", title, location.Department, location.Category, owner, updated))
		filler = []byte("Section: Summary\n- Prepared for leadership review.\n- Includes metrics and qualitative notes.\n- Collaborators should sign off before publishing.\n\n")
	case "xlsx":
		header = []byte(fmt.Sprintf("Workbook: %s\nDepartment: %s\nCategory: %s\nOwner: %s\nLast Updated: %s\n\nColumns: Metric, Target, Actual, Notes\n", title, location.Department, location.Category, owner, updated))
		filler = []byte("Metric,Target,Actual,Notes\nRevenue,100,95,Tracking toward goal\nSatisfaction,90,92,Ahead of expectations\n\n")
	case "pptx":
		header = []byte(fmt.Sprintf("Slide Deck: %s\nDepartment: %s\nCategory: %s\nPresenter: %s\nLast Updated: %s\n\n", title, location.Department, location.Category, owner, updated))
		filler = []byte("Slide 1 - Overview\nSlide 2 - Metrics\nSlide 3 - Roadmap\nSlide 4 - Next Steps\n\n")
	default:
		header = []byte(fmt.Sprintf("Title: %s\nDepartment: %s\nCategory: %s\nOwner: %s\nLast Updated: %s\n\n", title, location.Department, location.Category, owner, updated))
		filler = []byte("Generated by sampletool for office-style testing.\n")
	}

	if len(filler) == 0 {
		filler = []byte("Generated by sampletool for office-style testing.\n")
	}

	message := fmt.Sprintf(" - %s/%s", location.Department, location.Category)
	return header, filler, message
}

func formatTitle(name string) string {
	segments := strings.FieldsFunc(name, func(r rune) bool {
		return r == '-' || r == '_' || r == ' '
	})
	if len(segments) == 0 {
		return "Untitled Document"
	}
	for i, seg := range segments {
		segments[i] = capitalize(seg)
	}
	return strings.Join(segments, " ")
}

func capitalize(input string) string {
	if input == "" {
		return input
	}
	runes := []rune(strings.ToLower(input))
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

func slugify(input string) string {
	if input == "" {
		return ""
	}
	var b strings.Builder
	previousHyphen := false
	for _, r := range strings.ToLower(input) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			previousHyphen = false
			continue
		}
		if !previousHyphen {
			b.WriteRune('-')
			previousHyphen = true
		}
	}
	slug := b.String()
	slug = strings.Trim(slug, "-")
	return slug
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
