//go:build ignore

// check-docs verifies that the Markdown documentation still points at things
// that exist, so a rename or move in the code fails CI instead of leaving the
// docs quietly wrong. It checks, in every tracked *.md file:
//
//   - relative links and their #anchors (against GitHub's heading slugs);
//   - repository paths written in code spans, such as `internal/auth/token.go`
//     or `scripts/install.sh`;
//   - Go identifiers written in code spans as package.Name (`endpoint.Parse`)
//     or Type.Method (`Endpoint.Listen`) for this module's own packages.
//
// External URLs are not fetched. Run from the repository root:
//
//	go run scripts/check-docs.go
package main

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

var (
	inlineLink  = regexp.MustCompile(`\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	refLink     = regexp.MustCompile(`^\s*\[[^\]]+\]:\s*(\S+)`)
	codeSpan    = regexp.MustCompile("`([^`]+)`")
	repoPath    = regexp.MustCompile(`^(?:\.\./)*((?:cmd|internal|scripts|\.github)/[A-Za-z0-9_./-]+|[A-Za-z0-9_-]+\.(?:go|yaml|yml|sh))$`)
	qualifiedID = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\.([A-Za-z_][A-Za-z0-9_]*)(?:\(\))?$`)
	heading     = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*#*\s*$`)
	fence       = regexp.MustCompile("^\\s*(```|~~~)")
)

// goIndex holds the identifiers declared in this module, keyed by package
// name ("endpoint", "auth", "cmd", "main", ...).
type goIndex struct {
	pkgs    map[string]map[string]bool // package -> top-level names
	methods map[string]map[string]bool // receiver type -> methods and fields
}

func main() {
	files, err := trackedFiles()
	if err != nil {
		fatal(err)
	}
	idx, err := indexGo(files)
	if err != nil {
		fatal(err)
	}
	slugCache := map[string]map[string]bool{}
	var problems []string
	for _, f := range files {
		if !strings.HasSuffix(f, ".md") {
			continue
		}
		problems = append(problems, checkFile(f, idx, slugCache)...)
	}
	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Println(p)
		}
		fmt.Fprintf(os.Stderr, "check-docs: %d problem(s)\n", len(problems))
		os.Exit(1)
	}
	fmt.Println("check-docs: ok")
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "check-docs:", err)
	os.Exit(2)
}

func trackedFiles() ([]string, error) {
	out, err := exec.Command("git", "ls-files").Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	return strings.Fields(string(out)), nil
}

func indexGo(files []string) (*goIndex, error) {
	idx := &goIndex{pkgs: map[string]map[string]bool{}, methods: map[string]map[string]bool{}}
	fset := token.NewFileSet()
	for _, f := range files {
		if !strings.HasSuffix(f, ".go") || strings.HasSuffix(f, "_test.go") || strings.HasPrefix(f, "scripts/") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		names := idx.pkgs[file.Name.Name]
		if names == nil {
			names = map[string]bool{}
			idx.pkgs[file.Name.Name] = names
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil {
					names[d.Name.Name] = true
					continue
				}
				idx.addMember(receiverName(d.Recv.List[0].Type), d.Name.Name)
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.ValueSpec:
						for _, n := range s.Names {
							names[n.Name] = true
						}
					case *ast.TypeSpec:
						names[s.Name.Name] = true
						if st, ok := s.Type.(*ast.StructType); ok {
							for _, field := range st.Fields.List {
								for _, n := range field.Names {
									idx.addMember(s.Name.Name, n.Name)
								}
							}
						}
					}
				}
			}
		}
	}
	return idx, nil
}

func (idx *goIndex) addMember(typ, name string) {
	if idx.methods[typ] == nil {
		idx.methods[typ] = map[string]bool{}
	}
	idx.methods[typ][name] = true
}

func receiverName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return receiverName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return receiverName(t.X)
	}
	return ""
}

// checkFile reports every unresolvable reference in one Markdown file.
func checkFile(name string, idx *goIndex, slugCache map[string]map[string]bool) []string {
	data, err := os.ReadFile(name)
	if err != nil {
		return []string{fmt.Sprintf("%s: %v", name, err)}
	}
	var problems []string
	report := func(line int, format string, args ...any) {
		problems = append(problems, fmt.Sprintf("%s:%d: %s", name, line, fmt.Sprintf(format, args...)))
	}
	inFence := false
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if fence.MatchString(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		var targets []string
		for _, m := range inlineLink.FindAllStringSubmatch(line, -1) {
			targets = append(targets, m[1])
		}
		if m := refLink.FindStringSubmatch(line); m != nil {
			targets = append(targets, m[1])
		}
		for _, t := range targets {
			if msg := checkLink(name, t, slugCache); msg != "" {
				report(n, "%s", msg)
			}
		}
		for _, m := range codeSpan.FindAllStringSubmatch(line, -1) {
			if msg := checkCodeSpan(name, m[1], idx); msg != "" {
				report(n, "%s", msg)
			}
		}
	}
	return problems
}

func checkLink(from, target string, slugCache map[string]map[string]bool) string {
	if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
		return ""
	}
	file, anchor, _ := strings.Cut(target, "#")
	resolved := from
	if file != "" {
		resolved = path.Clean(path.Join(path.Dir(from), file))
		if _, err := os.Stat(resolved); err != nil {
			return fmt.Sprintf("link %q: %s does not exist", target, resolved)
		}
	}
	if anchor == "" {
		return ""
	}
	if !strings.HasSuffix(resolved, ".md") {
		return fmt.Sprintf("link %q: anchors are only checked in Markdown files", target)
	}
	slugs, ok := slugCache[resolved]
	if !ok {
		slugs = headingSlugs(resolved)
		slugCache[resolved] = slugs
	}
	if !slugs[anchor] {
		return fmt.Sprintf("link %q: no heading in %s has the anchor #%s", target, resolved, anchor)
	}
	return ""
}

// headingSlugs returns the anchors GitHub generates for a file's headings:
// lowercased, punctuation other than "-" and "_" removed, spaces turned into
// "-", and "-1", "-2", ... appended to repeats.
func headingSlugs(name string) map[string]bool {
	slugs := map[string]bool{}
	data, err := os.ReadFile(name)
	if err != nil {
		return slugs
	}
	seen := map[string]int{}
	inFence := false
	for _, line := range strings.Split(string(data), "\n") {
		if fence.MatchString(line) {
			inFence = !inFence
			continue
		}
		m := heading.FindStringSubmatch(line)
		if inFence || m == nil {
			continue
		}
		var b strings.Builder
		for _, r := range strings.ToLower(m[2]) {
			switch {
			case r == ' ':
				b.WriteRune('-')
			case r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r):
				b.WriteRune(r)
			}
		}
		slug := b.String()
		if k := seen[slug]; k > 0 {
			slugs[fmt.Sprintf("%s-%d", slug, k)] = true
		} else {
			slugs[slug] = true
		}
		seen[slug]++
	}
	return slugs
}

func checkCodeSpan(from, span string, idx *goIndex) string {
	span = strings.TrimSpace(span)
	if m := repoPath.FindStringSubmatch(span); m != nil {
		p := m[1]
		if strings.ContainsAny(p, "*<>{}$") {
			return ""
		}
		if !strings.Contains(p, "/") {
			// A bare file name such as `token.go`: accept it if any tracked
			// file has that base name.
			if matches, _ := filepath.Glob("*/" + p); len(matches) > 0 {
				return ""
			}
			if matches, _ := filepath.Glob("*/*/" + p); len(matches) > 0 {
				return ""
			}
			if _, err := os.Stat(p); err == nil {
				return ""
			}
			return fmt.Sprintf("code span %q: no file named %s in the repository", span, p)
		}
		if _, err := os.Stat(strings.TrimSuffix(p, "/")); err != nil {
			return fmt.Sprintf("code span %q: %s does not exist", span, p)
		}
		return ""
	}
	m := qualifiedID.FindStringSubmatch(span)
	if m == nil {
		return ""
	}
	qual, name := m[1], m[2]
	if names, ok := idx.pkgs[qual]; ok && qual != "main" {
		if !names[name] {
			return fmt.Sprintf("code span %q: package %s declares no %s", span, qual, name)
		}
		return ""
	}
	if members, ok := idx.methods[qual]; ok {
		if !members[name] {
			return fmt.Sprintf("code span %q: type %s has no method or field %s", span, qual, name)
		}
	}
	return ""
}
