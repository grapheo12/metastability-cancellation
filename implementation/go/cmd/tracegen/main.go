// Command tracegen fills and checks the traceability tables of every
// "Design specific to Go" section from the "Design:" tags in the Go code.
//
// Source: AGENTS.md#traceability-generator-tracegen
//
// Usage (from implementation/go):
//
//	CGO_ENABLED=0 go run ./cmd/tracegen -root ../..          # check: exit 1 if anything is missing or stale
//	CGO_ENABLED=0 go run ./cmd/tracegen -root ../.. -write   # rewrite the tables, then check
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	beginMarker = "<!-- trace:begin -->"
	endMarker   = "<!-- trace:end -->"
)

var (
	tagRe     = regexp.MustCompile(`Design:\s*(abstraction/\S+?design\.md)#(\S+)`)
	headingRe = regexp.MustCompile(`(?m)^## (.+)$`)
	slugDrop  = regexp.MustCompile(`[^\p{L}\p{N}_\- ]`)
)

type loc struct {
	file string // relative to the repo root, forward slashes
	line int
}

type key struct{ design, anchor string }

// slug is the GitHub heading anchor: lowercase, drop everything except
// letters, digits, '_', '-' and ' ', then turn spaces into '-'.
func slug(heading string) string {
	s := strings.ToLower(strings.TrimSpace(heading))
	return strings.ReplaceAll(slugDrop.ReplaceAllString(s, ""), " ", "-")
}

// collectTags maps (design file, anchor) to the code lines that implement it.
// A tag resolves to the first following line that is neither blank nor a comment.
func collectTags(root, implDir, ext, comment string) (map[key][]loc, error) {
	refs := map[key][]loc{}
	err := filepath.WalkDir(filepath.Join(root, implDir), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ext) {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		var pending []key
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for n := 1; sc.Scan(); n++ {
			s := strings.TrimSpace(sc.Text())
			isComment := strings.HasPrefix(s, comment)
			if m := tagRe.FindStringSubmatch(s); m != nil && isComment {
				pending = append(pending, key{m[1], m[2]})
				continue
			}
			if len(pending) > 0 && s != "" && !isComment {
				for _, k := range pending {
					refs[k] = append(refs[k], loc{rel, n})
				}
				pending = nil
			}
		}
		return sc.Err()
	})
	return refs, err
}

// designFiles returns every design.md that has a language section or is tagged.
func designFiles(root, sectionHeader string, refs map[key][]loc) ([]string, error) {
	set := map[string]bool{}
	for k := range refs {
		set[k.design] = true
	}
	err := filepath.WalkDir(filepath.Join(root, "abstraction"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "design.md" {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if strings.Contains(string(b), sectionHeader) {
			rel, _ := filepath.Rel(root, p)
			set[filepath.ToSlash(rel)] = true
		}
		return nil
	})
	var out []string
	for d := range set {
		out = append(out, d)
	}
	sort.Strings(out)
	return out, err
}

// buildTable renders one row per spec subsection (the "## " headings before
// the language section), with links relative to the design file.
func buildTable(root, design, specPart string, refs map[key][]loc, problem func(string, ...any)) string {
	toRoot, _ := filepath.Rel(filepath.Dir(filepath.Join(root, design)), root)
	toRoot = filepath.ToSlash(toRoot)
	rows := []string{"| Design subsection | Implementation (file:line) |", "|---|---|"}
	for _, m := range headingRe.FindAllStringSubmatch(specPart, -1) {
		h, a := m[1], slug(m[1])
		locs := dedupSorted(refs[key{design, a}])
		if len(locs) == 0 {
			problem("UNCOVERED %s#%s", design, a)
		}
		links := make([]string, len(locs))
		for i, l := range locs {
			links[i] = fmt.Sprintf("[%s:%d](%s/%s#L%d)", filepath.Base(l.file), l.line, toRoot, l.file, l.line)
		}
		rows = append(rows, fmt.Sprintf("| [%s](#%s) | %s |", h, a, strings.Join(links, ", ")))
	}
	return strings.Join(rows, "\n")
}

func dedupSorted(ls []loc) []loc {
	sort.Slice(ls, func(i, j int) bool {
		if ls[i].file != ls[j].file {
			return ls[i].file < ls[j].file
		}
		return ls[i].line < ls[j].line
	})
	var out []loc
	for i, l := range ls {
		if i == 0 || l != ls[i-1] {
			out = append(out, l)
		}
	}
	return out
}

func main() {
	root := flag.String("root", ".", "repository root")
	implDir := flag.String("impl", "implementation/go", "implementation directory, relative to root")
	lang := flag.String("lang", "Go", "language name used in the section header")
	ext := flag.String("ext", ".go", "source file extension to scan")
	comment := flag.String("comment", "//", "line comment prefix")
	write := flag.Bool("write", false, "rewrite the traceability tables")
	flag.Parse()

	ok := true
	problem := func(format string, a ...any) {
		fmt.Printf(format+"\n", a...)
		ok = false
	}
	sectionHeader := "## Design specific to " + *lang

	refs, err := collectTags(*root, *implDir, *ext, *comment)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	designs, err := designFiles(*root, sectionHeader, refs)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	for _, design := range designs {
		path := filepath.Join(*root, design)
		b, err := os.ReadFile(path)
		if err != nil {
			problem("MISSING FILE %s", design)
			continue
		}
		text := string(b)
		specPart := strings.SplitN(text, sectionHeader, 2)[0]
		anchors := map[string]bool{}
		for _, m := range headingRe.FindAllStringSubmatch(specPart, -1) {
			anchors[slug(m[1])] = true
		}
		var unknown []string
		for k := range refs {
			if k.design == design && !anchors[k.anchor] {
				unknown = append(unknown, k.anchor)
			}
		}
		sort.Strings(unknown)
		for _, a := range unknown {
			problem("UNKNOWN ANCHOR %s#%s", design, a)
		}
		table := buildTable(*root, design, specPart, refs, problem)
		i, j := strings.Index(text, beginMarker), strings.Index(text, endMarker)
		if !strings.Contains(text, sectionHeader) || i < 0 || j < i {
			problem("NO SECTION %s", design)
			continue
		}
		updated := text[:i] + beginMarker + "\n" + table + "\n" + text[j:]
		if updated == text {
			continue
		}
		if *write {
			if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
				problem("WRITE FAILED %s: %v", design, err)
			}
		} else {
			problem("STALE %s", design)
		}
	}
	if !ok {
		fmt.Println("PROBLEMS")
		os.Exit(1)
	}
	fmt.Println("OK")
}
