package main

import (
	"archive/zip"
	"bytes"
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
	message string
	content []byte
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

		content, message, err := buildFileContent(ext, location, name, size)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to build content for %s: %v\n", path, err)
			os.Exit(1)
		}
		actualSize := int64(len(content))
		if actualSize == 0 {
			fmt.Fprintf(os.Stderr, "no content generated for %s\n", path)
			os.Exit(1)
		}
		if maxBytes > 0 && totalBytes+actualSize > maxBytes {
			fmt.Printf("Reached maximum total bytes (%s). Stopping.\n", humanReadable(maxBytes))
			break
		}
		tasks = append(tasks, fileTask{
			path:    path,
			size:    actualSize,
			index:   len(tasks),
			message: message,
			content: content,
		})
		totalBytes += actualSize
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
				if err := createFile(task); err != nil {
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

func createFile(task fileTask) error {
	if task.size <= 0 {
		return errors.New("size must be positive")
	}
	f, err := os.Create(task.path)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := f.Write(task.content); err != nil {
		return err
	}
	return f.Sync()
}

func buildFileContent(ext string, location officeLocation, name string, targetSize int64) ([]byte, string, error) {
	owner := teamMembers[mrand.Intn(len(teamMembers))]
	updated := time.Now().Add(-time.Duration(mrand.Intn(120)) * 24 * time.Hour).Format("2006-01-02")
	title := formatTitle(name)

	meta := documentMetadata{
		Title:      title,
		Department: location.Department,
		Category:   location.Category,
		Owner:      owner,
		Updated:    updated,
	}

	var (
		content []byte
		err     error
	)
	switch strings.ToLower(ext) {
	case "csv":
		content = buildCSVContent(meta, targetSize)
	case "md":
		content = buildMarkdownContent(meta, targetSize)
	case "txt":
		content = buildPlainTextContent(meta, targetSize)
	case "pdf":
		content, err = buildPDFContent(meta, targetSize)
	case "docx":
		content, err = buildDocxContent(meta, targetSize)
	case "xlsx":
		content, err = buildXLSXContent(meta, targetSize)
	case "pptx":
		content, err = buildPPTXContent(meta, targetSize)
	default:
		content = buildDefaultContent(meta, targetSize)
	}

	if err != nil {
		return nil, "", err
	}
	if len(content) == 0 {
		content = buildDefaultContent(meta, targetSize)
	}

	message := fmt.Sprintf(" - %s/%s", location.Department, location.Category)
	return content, message, nil
}

type documentMetadata struct {
	Title      string
	Department string
	Category   string
	Owner      string
	Updated    string
}

func buildCSVContent(meta documentMetadata, target int64) []byte {
	header := "Title,Department,Category,Owner,LastUpdated,Summary\n"
	baseRows := []string{
		fmt.Sprintf("%s,%s,%s,%s,%s,%s\n", meta.Title, meta.Department, meta.Category, meta.Owner, meta.Updated, "Status update recorded"),
		fmt.Sprintf("Quarterly Forecast,%s,%s,%s,%s,%s\n", meta.Department, meta.Category, meta.Owner, meta.Updated, "Pipeline and revenue outlook"),
		fmt.Sprintf("Team Health,%s,%s,%s,%s,%s\n", meta.Department, meta.Category, meta.Owner, meta.Updated, "Engagement scores and staffing"),
	}
	base := header + strings.Join(baseRows, "")
	filler := fmt.Sprintf("Detail,%s,%s,%s,%s,%s\n", meta.Department, meta.Category, meta.Owner, meta.Updated, "Action items and blockers summarised")
	return fillContent(base, filler, target)
}

func buildMarkdownContent(meta documentMetadata, target int64) []byte {
	base := fmt.Sprintf(`# %s

- **Department:** %s
- **Category:** %s
- **Owner:** %s
- **Last Updated:** %s

## Highlights

- Roadmap checkpoints reviewed with stakeholders.
- Budget alignment confirmed for upcoming initiatives.
- Risks and mitigations updated for the leadership team.

## Notes

The %s group is coordinating across teams to ensure deliverables remain on track and well-communicated.

`, meta.Title, meta.Department, meta.Category, meta.Owner, meta.Updated, meta.Department)
	filler := fmt.Sprintf("- Additional insight for %s/%s coordinated by %s.\n", meta.Department, meta.Category, meta.Owner)
	return fillContent(base, filler, target)
}

func buildPlainTextContent(meta documentMetadata, target int64) []byte {
	base := fmt.Sprintf(`Title: %s
Department: %s
Category: %s
Owner: %s
Last Updated: %s

Summary:
- Primary initiatives evaluated and prioritised.
- Coordination with partner teams scheduled for the upcoming sprint.
- Stakeholder communications drafted for weekly circulation.

Action Items:
- Review deliverables for %s.
- Share updates with leadership and partners.
- Archive supporting documents in the team workspace.

`, meta.Title, meta.Department, meta.Category, meta.Owner, meta.Updated, meta.Department)
	filler := fmt.Sprintf("Follow-up: ensure %s provides feedback on %s updates.\n", meta.Owner, meta.Category)
	return fillContent(base, filler, target)
}

func buildDefaultContent(meta documentMetadata, target int64) []byte {
	base := fmt.Sprintf(`Title: %s
Department: %s
Category: %s
Owner: %s
Last Updated: %s

Generated by sampletool for office-style testing scenarios.
`, meta.Title, meta.Department, meta.Category, meta.Owner, meta.Updated)
	filler := fmt.Sprintf("Reference update for %s/%s prepared by %s.\n", meta.Department, meta.Category, meta.Owner)
	return fillContent(base, filler, target)
}

func buildPDFContent(meta documentMetadata, target int64) ([]byte, error) {
	baseLines := []string{
		fmt.Sprintf("%s — %s", meta.Department, meta.Category),
		fmt.Sprintf("Owner: %s", meta.Owner),
		fmt.Sprintf("Last Updated: %s", meta.Updated),
		"Summary of current initiatives:",
		fmt.Sprintf("• Key focus areas for the %s organisation.", meta.Department),
	}

	fillerLen := 0
	filler := ""
	stream := buildPDFStream(baseLines, filler)
	data, err := finalizePDFDocument(stream)
	if err != nil {
		return nil, err
	}
	if target <= 0 {
		return data, nil
	}

	for attempt := 0; attempt < 10; attempt++ {
		size := int64(len(data))
		if size == target {
			break
		}
		if size > target && fillerLen > 0 {
			reduce := int(size - target)
			if reduce >= fillerLen {
				fillerLen = 0
			} else {
				fillerLen -= reduce
			}
		} else if size < target {
			fillerLen += int(target - size)
		} else {
			break
		}
		filler = buildPlainFiller(fillerLen)
		stream = buildPDFStream(baseLines, filler)
		data, err = finalizePDFDocument(stream)
		if err != nil {
			return nil, err
		}
	}
	return data, nil
}

func buildDocxContent(meta documentMetadata, target int64) ([]byte, error) {
	fillerLen := 0
	filler := ""
	data, err := writeDocx(meta, filler)
	if err != nil {
		return nil, err
	}
	if target <= 0 {
		return data, nil
	}

	for attempt := 0; attempt < 10; attempt++ {
		size := int64(len(data))
		if size == target {
			break
		}
		if size > target && fillerLen > 0 {
			reduce := int(size - target)
			if reduce >= fillerLen {
				fillerLen = 0
			} else {
				fillerLen -= reduce
			}
		} else if size < target {
			fillerLen += int(target - size)
		} else {
			break
		}
		filler = buildPlainFiller(fillerLen)
		data, err = writeDocx(meta, filler)
		if err != nil {
			return nil, err
		}
	}
	return data, nil
}

func buildXLSXContent(meta documentMetadata, target int64) ([]byte, error) {
	fillerLen := 0
	filler := ""
	data, err := writeXLSX(meta, filler)
	if err != nil {
		return nil, err
	}
	if target <= 0 {
		return data, nil
	}

	for attempt := 0; attempt < 10; attempt++ {
		size := int64(len(data))
		if size == target {
			break
		}
		if size > target && fillerLen > 0 {
			reduce := int(size - target)
			if reduce >= fillerLen {
				fillerLen = 0
			} else {
				fillerLen -= reduce
			}
		} else if size < target {
			fillerLen += int(target - size)
		} else {
			break
		}
		filler = buildPlainFiller(fillerLen)
		data, err = writeXLSX(meta, filler)
		if err != nil {
			return nil, err
		}
	}
	return data, nil
}

func buildPPTXContent(meta documentMetadata, target int64) ([]byte, error) {
	fillerLen := 0
	filler := ""
	data, err := writePPTX(meta, filler)
	if err != nil {
		return nil, err
	}
	if target <= 0 {
		return data, nil
	}

	for attempt := 0; attempt < 10; attempt++ {
		size := int64(len(data))
		if size == target {
			break
		}
		if size > target && fillerLen > 0 {
			reduce := int(size - target)
			if reduce >= fillerLen {
				fillerLen = 0
			} else {
				fillerLen -= reduce
			}
		} else if size < target {
			fillerLen += int(target - size)
		} else {
			break
		}
		filler = buildPlainFiller(fillerLen)
		data, err = writePPTX(meta, filler)
		if err != nil {
			return nil, err
		}
	}
	return data, nil
}

func buildPDFStream(baseLines []string, filler string) string {
	var sb strings.Builder
	sb.WriteString("BT\n")
	sb.WriteString("/F1 12 Tf\n")
	sb.WriteString("72 720 Td\n")
	for i, line := range baseLines {
		if i > 0 {
			sb.WriteString("T*\n")
		}
		sb.WriteString("(")
		sb.WriteString(escapePDFText(line))
		sb.WriteString(") Tj\n")
	}
	if filler != "" {
		for _, chunk := range chunkString(filler, 80) {
			sb.WriteString("T*\n(")
			sb.WriteString(escapePDFText(chunk))
			sb.WriteString(") Tj\n")
		}
	}
	sb.WriteString("ET")
	return sb.String()
}

func finalizePDFDocument(stream string) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n%âãÏÓ\n")

	objects := []string{
		"1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj\n",
		"2 0 obj << /Type /Pages /Kids [3 0 R] /Count 1 >> endobj\n",
		"3 0 obj << /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >> endobj\n",
		fmt.Sprintf("4 0 obj << /Length %d >> stream\n%s\nendstream\nendobj\n", len(stream), stream),
		"5 0 obj << /Type /Font /Subtype /Type1 /BaseFont /Helvetica >> endobj\n",
	}

	offsets := make([]int, 0, len(objects)+1)
	for _, obj := range objects {
		offsets = append(offsets, buf.Len())
		buf.WriteString(obj)
	}

	xrefStart := buf.Len()
	buf.WriteString("xref\n")
	fmt.Fprintf(&buf, "0 %d\n", len(objects)+1)
	buf.WriteString("0000000000 65535 f \n")
	for _, off := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	buf.WriteString("trailer << /Size ")
	fmt.Fprintf(&buf, "%d /Root 1 0 R >>\n", len(objects)+1)
	buf.WriteString("startxref\n")
	fmt.Fprintf(&buf, "%d\n", xrefStart)
	buf.WriteString("%%EOF\n")

	return buf.Bytes(), nil
}

func writeDocx(meta documentMetadata, filler string) ([]byte, error) {
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	now := time.Now().UTC().Format(time.RFC3339)

	files := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
  <Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>
  <Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/>
</Types>
`,
		"_rels/.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
  <Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>
  <Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties" Target="docProps/app.xml"/>
</Relationships>
`,
		"docProps/app.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties" xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes">
  <Application>sampletool</Application>
  <Company>Internal</Company>
  <Pages>1</Pages>
</Properties>
`,
		"docProps/core.xml": fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:dcmitype="http://purl.org/dc/dcmitype/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
  <dc:title>%s</dc:title>
  <dc:subject>%s</dc:subject>
  <dc:creator>%s</dc:creator>
  <cp:lastModifiedBy>sampletool</cp:lastModifiedBy>
  <dcterms:created xsi:type="dcterms:W3CDTF">%s</dcterms:created>
  <dcterms:modified xsi:type="dcterms:W3CDTF">%s</dcterms:modified>
</cp:coreProperties>
`, xmlEscape(meta.Title), xmlEscape(meta.Category), xmlEscape(meta.Owner), now, now),
		"word/_rels/document.xml.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"/>
`,
	}

	for name, content := range files {
		if err := addStoredFile(zw, name, []byte(content)); err != nil {
			return nil, err
		}
	}

	docXML := buildDocxXML(meta, filler)
	if err := addStoredFile(zw, "word/document.xml", []byte(docXML)); err != nil {
		return nil, err
	}

	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func buildDocxXML(meta documentMetadata, filler string) string {
	var body strings.Builder
	addParagraph := func(text string) {
		body.WriteString("    <w:p><w:r><w:t xml:space=\"preserve\">")
		body.WriteString(xmlEscape(text))
		body.WriteString("</w:t></w:r></w:p>\n")
	}

	addParagraph(meta.Title)
	addParagraph(fmt.Sprintf("Department: %s", meta.Department))
	addParagraph(fmt.Sprintf("Category: %s", meta.Category))
	addParagraph(fmt.Sprintf("Owner: %s", meta.Owner))
	addParagraph(fmt.Sprintf("Last Updated: %s", meta.Updated))
	addParagraph("Summary prepared for collaboration and review.")

	if filler != "" {
		for _, chunk := range chunkString(filler, 180) {
			addParagraph(chunk)
		}
	}

	body.WriteString("    <w:sectPr><w:pgSz w:w=\"12240\" w:h=\"15840\"/><w:pgMar w:top=\"1440\" w:right=\"1440\" w:bottom=\"1440\" w:left=\"1440\"/></w:sectPr>\n")

	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
%s  </w:body>
</w:document>
`, body.String())
}

func writeXLSX(meta documentMetadata, filler string) ([]byte, error) {
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	now := time.Now().UTC().Format(time.RFC3339)

	files := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>
  <Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>
  <Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>
  <Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>
  <Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/>
</Types>
`,
		"_rels/.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>
  <Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>
  <Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties" Target="docProps/app.xml"/>
</Relationships>
`,
		"docProps/app.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties" xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes">
  <Application>sampletool</Application>
  <Company>Internal</Company>
  <Sheets>1</Sheets>
</Properties>
`,
		"docProps/core.xml": fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:dcmitype="http://purl.org/dc/dcmitype/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
  <dc:title>%s</dc:title>
  <dc:creator>%s</dc:creator>
  <cp:lastModifiedBy>sampletool</cp:lastModifiedBy>
  <dcterms:created xsi:type="dcterms:W3CDTF">%s</dcterms:created>
  <dcterms:modified xsi:type="dcterms:W3CDTF">%s</dcterms:modified>
</cp:coreProperties>
`, xmlEscape(meta.Title), xmlEscape(meta.Owner), now, now),
		"xl/_rels/workbook.xml.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>
</Relationships>
`,
		"xl/workbook.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <sheets>
    <sheet name="Summary" sheetId="1" r:id="rId1"/>
  </sheets>
</workbook>
`,
		"xl/styles.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
  <fonts count="1"><font><name val="Calibri"/><family val="2"/></font></fonts>
  <fills count="1"><fill><patternFill patternType="none"/></fill></fills>
  <borders count="1"><border/></borders>
  <cellStyleXfs count="1"><xf/></cellStyleXfs>
  <cellXfs count="1"><xf/></cellXfs>
</styleSheet>
`,
	}

	for name, content := range files {
		if err := addStoredFile(zw, name, []byte(content)); err != nil {
			return nil, err
		}
	}

	sheet := buildXLSXSheet(meta, filler)
	if err := addStoredFile(zw, "xl/worksheets/sheet1.xml", []byte(sheet)); err != nil {
		return nil, err
	}

	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func buildXLSXSheet(meta documentMetadata, filler string) string {
	var rows strings.Builder
	writeCell := func(col string, row int, value string) {
		rows.WriteString(fmt.Sprintf("      <c r=\"%s%d\" t=\"inlineStr\"><is><t xml:space=\"preserve\">%s</t></is></c>\n", col, row, xmlEscape(value)))
	}

	writeRow := func(index int, values []string) {
		rows.WriteString(fmt.Sprintf("    <row r=\"%d\">\n", index))
		for i, v := range values {
			col := string(rune('A' + i))
			writeCell(col, index, v)
		}
		rows.WriteString("    </row>\n")
	}

	writeRow(1, []string{"Title", "Department", "Category", "Owner", "Last Updated"})
	writeRow(2, []string{meta.Title, meta.Department, meta.Category, meta.Owner, meta.Updated})
	writeRow(3, []string{"Focus", "Status", "Target", "Actual", "Notes"})
	writeRow(4, []string{"Roadmap", "Active", "On Track", "Slight Drift", "Monitoring milestones"})
	writeRow(5, []string{"Engagement", "Stable", "Above 85", "88", "Team morale improving"})

	if filler != "" {
		chunked := chunkString(filler, 120)
		baseRow := 6
		for i, chunk := range chunked {
			writeRow(baseRow+i, []string{fmt.Sprintf("Narrative %d", i+1), "Detail", "", "", chunk})
		}
	}

	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
  <sheetData>
%s  </sheetData>
</worksheet>
`, rows.String())
}

func writePPTX(meta documentMetadata, filler string) ([]byte, error) {
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	now := time.Now().UTC().Format(time.RFC3339)

	files := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/ppt/presentation.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"/>
  <Override PartName="/ppt/slides/slide1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slide+xml"/>
  <Override PartName="/ppt/theme/theme1.xml" ContentType="application/vnd.openxmlformats-officedocument.theme+xml"/>
  <Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>
  <Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/>
</Types>
`,
		"_rels/.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="ppt/presentation.xml"/>
  <Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>
  <Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties" Target="docProps/app.xml"/>
</Relationships>
`,
		"docProps/app.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties" xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes">
  <Application>sampletool</Application>
  <PresentationFormat>Office</PresentationFormat>
  <Slides>1</Slides>
</Properties>
`,
		"docProps/core.xml": fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:dcmitype="http://purl.org/dc/dcmitype/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
  <dc:title>%s</dc:title>
  <dc:creator>%s</dc:creator>
  <cp:lastModifiedBy>sampletool</cp:lastModifiedBy>
  <dcterms:created xsi:type="dcterms:W3CDTF">%s</dcterms:created>
  <dcterms:modified xsi:type="dcterms:W3CDTF">%s</dcterms:modified>
</cp:coreProperties>
`, xmlEscape(meta.Title), xmlEscape(meta.Owner), now, now),
		"ppt/_rels/presentation.xml.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide1.xml"/>
  <Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/theme" Target="theme/theme1.xml"/>
</Relationships>
`,
		"ppt/presentation.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<p:presentation xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <p:sldIdLst>
    <p:sldId id="256" r:id="rId1"/>
  </p:sldIdLst>
  <p:sldSz cx="9144000" cy="6858000" type="screen4x3"/>
  <p:notesSz cx="6858000" cy="9144000"/>
</p:presentation>
`,
		"ppt/theme/theme1.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<a:theme xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" name="Office Theme">
  <a:themeElements>
    <a:clrScheme name="Office">
      <a:dk1><a:sysClr val="windowText" lastClr="000000"/></a:dk1>
      <a:lt1><a:sysClr val="window" lastClr="FFFFFF"/></a:lt1>
    </a:clrScheme>
    <a:fontScheme name="Office">
      <a:majorFont>
        <a:latin typeface="Calibri"/>
      </a:majorFont>
      <a:minorFont>
        <a:latin typeface="Calibri"/>
      </a:minorFont>
    </a:fontScheme>
    <a:fmtScheme name="Office">
      <a:fillStyleLst/>
      <a:lnStyleLst/>
      <a:effectStyleLst/>
      <a:bgFillStyleLst/>
    </a:fmtScheme>
  </a:themeElements>
</a:theme>
`,
	}

	for name, content := range files {
		if err := addStoredFile(zw, name, []byte(content)); err != nil {
			return nil, err
		}
	}

	slide := buildPPTXSlide(meta, filler)
	if err := addStoredFile(zw, "ppt/slides/slide1.xml", []byte(slide)); err != nil {
		return nil, err
	}

	slideRels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"/>
`
	if err := addStoredFile(zw, "ppt/slides/_rels/slide1.xml.rels", []byte(slideRels)); err != nil {
		return nil, err
	}

	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func buildPPTXSlide(meta documentMetadata, filler string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("<a:t>%s</a:t>", xmlEscape(meta.Title)))
	bulletLines := []string{
		fmt.Sprintf("Department: %s", meta.Department),
		fmt.Sprintf("Category: %s", meta.Category),
		fmt.Sprintf("Owner: %s", meta.Owner),
		fmt.Sprintf("Last Updated: %s", meta.Updated),
	}
	if filler != "" {
		for i, chunk := range chunkString(filler, 90) {
			bulletLines = append(bulletLines, fmt.Sprintf("Detail %d: %s", i+1, chunk))
		}
	}

	var body strings.Builder
	for _, line := range bulletLines {
		body.WriteString("          <a:p>\n")
		body.WriteString("            <a:r><a:rPr lang=\"en-US\" dirty=\"0\" smtClean=\"0\"/><a:t>")
		body.WriteString(xmlEscape(line))
		body.WriteString("</a:t></a:r>\n")
		body.WriteString("          </a:p>\n")
	}

	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <p:cSld>
    <p:spTree>
      <p:nvGrpSpPr>
        <p:cNvPr id="1" name=""/>
        <p:cNvGrpSpPr/>
        <p:nvPr/>
      </p:nvGrpSpPr>
      <p:grpSpPr><a:xfrm/></p:grpSpPr>
      <p:sp>
        <p:nvSpPr>
          <p:cNvPr id="2" name="Title 1"/>
          <p:cNvSpPr/>
          <p:nvPr/>
        </p:nvSpPr>
        <p:spPr/>
        <p:txBody>
          <a:bodyPr/>
          <a:lstStyle/>
          <a:p>
            <a:r><a:t>%s</a:t></a:r>
          </a:p>
        </p:txBody>
      </p:sp>
      <p:sp>
        <p:nvSpPr>
          <p:cNvPr id="3" name="Content Placeholder 2"/>
          <p:cNvSpPr/>
          <p:nvPr/>
        </p:nvSpPr>
        <p:spPr/>
        <p:txBody>
          <a:bodyPr/>
          <a:lstStyle/>
%s        </p:txBody>
      </p:sp>
    </p:spTree>
  </p:cSld>
  <p:clrMapOvr>
    <a:masterClrMapping/>
  </p:clrMapOvr>
</p:sld>
`, xmlEscape(meta.Title), body.String())
}

func addStoredFile(zw *zip.Writer, name string, data []byte) error {
	header := &zip.FileHeader{Name: name, Method: zip.Store}
	header.SetModTime(time.Now())
	w, err := zw.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func xmlEscape(input string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(input)
}

func escapePDFText(input string) string {
	replacer := strings.NewReplacer(
		"\\", "\\\\",
		"(", "\\(",
		")", "\\)",
	)
	return replacer.Replace(input)
}

func chunkString(input string, size int) []string {
	if size <= 0 {
		return []string{input}
	}
	var chunks []string
	for start := 0; start < len(input); start += size {
		end := start + size
		if end > len(input) {
			end = len(input)
		}
		chunks = append(chunks, input[start:end])
	}
	return chunks
}

func fillContent(base, filler string, target int64) []byte {
	if target <= 0 {
		target = int64(len(base))
	}
	baseLen := int64(len(base))
	if baseLen >= target {
		limit := int(target)
		if limit < 0 || limit > len(base) {
			limit = len(base)
		}
		return []byte(base[:limit])
	}
	if filler == "" {
		filler = base
	}
	buf := bytes.NewBuffer(make([]byte, 0, target))
	buf.WriteString(base)
	for int64(buf.Len()) < target {
		remaining := target - int64(buf.Len())
		if remaining >= int64(len(filler)) {
			buf.WriteString(filler)
		} else {
			buf.WriteString(filler[:remaining])
			break
		}
	}
	return buf.Bytes()
}

func buildPlainFiller(length int) string {
	if length <= 0 {
		return ""
	}
	pattern := "Project update timeline metrics collaboration insights "
	var sb strings.Builder
	sb.Grow(length)
	for sb.Len() < length {
		remaining := length - sb.Len()
		if remaining >= len(pattern) {
			sb.WriteString(pattern)
		} else {
			sb.WriteString(pattern[:remaining])
		}
	}
	return sb.String()
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
