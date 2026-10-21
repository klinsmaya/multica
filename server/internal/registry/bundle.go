package registry

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// A registry package is a plugin in the neutral Claude Code layout:
//
//	.claude-plugin/plugin.json   name, version, description, author, keywords, metadata
//	skills/<name>/SKILL.md       Agent Skills standard
//	agents/<name>.md             Markdown only
//	.mcp.json                    URL servers only
//
// Consumer-specific settings live under plugin.json's metadata.<consumer>,
// which other consumers ignore. The registry validates structure only; what a
// consumer can actually install is the consumer adapter's decision.
//
// Nothing in a package is executed by the registry or by Multica, so the
// layouts that exist to run code on the installing machine (hooks/,
// commands/, command-type MCP servers) are refused at upload.

const (
	ManifestPath  = ".claude-plugin/plugin.json"
	MCPConfigPath = ".mcp.json"

	MaxArchiveSize   = 4 << 20
	MaxFileSize      = 1 << 20
	MaxTotalSize     = 8 << 20
	MaxArchiveFiles  = 512
	maxSkillNameLen  = 64
	maxSkillDescLen  = 1024
	maxManifestBytes = 64 * 1024
)

var (
	packageNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
	skillNamePattern   = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	agentNamePattern   = regexp.MustCompile(`^[a-z0-9]+(?:[-_][a-z0-9]+)*$`)
	semverPattern      = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)
	filePathPattern    = regexp.MustCompile(`^[A-Za-z0-9._ -]+(?:/[A-Za-z0-9._ -]+)*$`)
)

// PluginManifest is the subset of plugin.json the registry reads. The full
// object is kept as RawManifest so fields the registry does not know survive.
type PluginManifest struct {
	Name        string                     `json:"name"`
	Version     string                     `json:"version"`
	DisplayName string                     `json:"displayName,omitempty"`
	Description string                     `json:"description,omitempty"`
	Author      *Person                    `json:"author,omitempty"`
	Homepage    string                     `json:"homepage,omitempty"`
	Repository  string                     `json:"repository,omitempty"`
	License     string                     `json:"license,omitempty"`
	Keywords    []string                   `json:"keywords,omitempty"`
	Metadata    map[string]json.RawMessage `json:"metadata,omitempty"`
}

type Person struct {
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
	URL   string `json:"url,omitempty"`
}

// File is one package file, with a root-relative path.
type File struct {
	Path    string
	Content []byte
}

type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path"`
}

type MCPServer struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
}

// Package is a parsed, validated registry package.
type Package struct {
	Manifest    PluginManifest
	RawManifest []byte
	Files       []File
	Skills      []Skill
	Agents      []string
	MCPServers  []MCPServer
	// Archive is the canonical zip consumers download: the package files at
	// the archive root, sorted, with fixed timestamps.
	Archive []byte
	SHA256  string
}

// File returns one file's content by root-relative path.
func (p Package) File(name string) ([]byte, bool) {
	for _, file := range p.Files {
		if file.Path == name {
			return file.Content, true
		}
	}
	return nil, false
}

// ParseArchive reads an uploaded zip. The plugin root may be the archive root
// or a single top-level directory, as Claude Code accepts.
func ParseArchive(archive []byte) (Package, error) {
	if len(archive) == 0 {
		return Package{}, fmt.Errorf("package is empty")
	}
	if len(archive) > MaxArchiveSize {
		return Package{}, fmt.Errorf("package exceeds %d bytes", MaxArchiveSize)
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return Package{}, fmt.Errorf("package must be a zip archive: %w", err)
	}
	if len(reader.File) > MaxArchiveFiles {
		return Package{}, fmt.Errorf("package contains more than %d entries", MaxArchiveFiles)
	}

	prefix := ""
	if !hasZipEntry(reader, ManifestPath) {
		for _, file := range reader.File {
			if dir, rest, ok := strings.Cut(file.Name, "/"); ok && rest == ManifestPath {
				prefix = dir + "/"
				break
			}
		}
		if prefix == "" {
			return Package{}, fmt.Errorf("package must contain %s at its root or one directory down", ManifestPath)
		}
	}

	files := make([]File, 0, len(reader.File))
	total := 0
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		name, ok := strings.CutPrefix(entry.Name, prefix)
		if !ok || ignoredPath(name) {
			continue
		}
		if entry.UncompressedSize64 > MaxFileSize {
			return Package{}, fmt.Errorf("file %q exceeds %d bytes", name, MaxFileSize)
		}
		rc, err := entry.Open()
		if err != nil {
			return Package{}, fmt.Errorf("read %q: %w", name, err)
		}
		content, err := io.ReadAll(io.LimitReader(rc, MaxFileSize+1))
		rc.Close()
		if err != nil {
			return Package{}, fmt.Errorf("read %q: %w", name, err)
		}
		if len(content) > MaxFileSize {
			return Package{}, fmt.Errorf("file %q exceeds %d bytes", name, MaxFileSize)
		}
		total += len(content)
		if total > MaxTotalSize {
			return Package{}, fmt.Errorf("package files exceed %d bytes", MaxTotalSize)
		}
		files = append(files, File{Path: name, Content: content})
	}
	return ParseFiles(files)
}

func hasZipEntry(reader *zip.Reader, name string) bool {
	for _, file := range reader.File {
		if file.Name == name {
			return true
		}
	}
	return false
}

// ignoredPath drops archiver noise rather than refusing a package over it.
func ignoredPath(name string) bool {
	return strings.HasPrefix(name, "__MACOSX/") || path.Base(name) == ".DS_Store"
}

// ParseFiles validates a package from its files. Uploads and stored versions
// go through the same function, so a consumer adapter re-reading a stored
// version sees exactly what was validated at upload.
func ParseFiles(files []File) (Package, error) {
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	seen := map[string]bool{}
	for _, file := range files {
		if err := validateFilePath(file.Path); err != nil {
			return Package{}, err
		}
		if seen[file.Path] {
			return Package{}, fmt.Errorf("duplicate file %q", file.Path)
		}
		seen[file.Path] = true
		top, _, _ := strings.Cut(file.Path, "/")
		switch top {
		case "hooks", "commands":
			return Package{}, fmt.Errorf("%s/ is not allowed: registry packages cannot run code on the installing machine", top)
		}
	}

	pkg := Package{Files: files}
	raw, ok := pkg.File(ManifestPath)
	if !ok {
		return Package{}, fmt.Errorf("package must contain %s", ManifestPath)
	}
	manifest, canonical, err := parseManifest(raw)
	if err != nil {
		return Package{}, err
	}
	pkg.Manifest = manifest
	pkg.RawManifest = canonical

	skills := map[string]*Skill{}
	for _, file := range files {
		parts := strings.Split(file.Path, "/")
		switch parts[0] {
		case "skills":
			if len(parts) < 3 {
				return Package{}, fmt.Errorf("%s: skill files must live in skills/<name>/", file.Path)
			}
			name := parts[1]
			if skills[name] == nil {
				skills[name] = &Skill{Name: name}
			}
			if len(parts) == 3 && parts[2] == "SKILL.md" {
				skills[name].Path = file.Path
				if err := parseSkill(skills[name], file.Content); err != nil {
					return Package{}, err
				}
			}
		case "agents":
			if len(parts) != 2 || !strings.HasSuffix(parts[1], ".md") {
				return Package{}, fmt.Errorf("%s: agents/ may only contain Markdown files", file.Path)
			}
			name := strings.TrimSuffix(parts[1], ".md")
			if !agentNamePattern.MatchString(name) {
				return Package{}, fmt.Errorf("%s: agent names use lowercase letters, digits, - and _", file.Path)
			}
			if !utf8.Valid(file.Content) {
				return Package{}, fmt.Errorf("%s is not UTF-8 text", file.Path)
			}
			pkg.Agents = append(pkg.Agents, name)
		}
	}
	names := make([]string, 0, len(skills))
	for name := range skills {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		skill := skills[name]
		if skill.Path == "" {
			return Package{}, fmt.Errorf("skills/%s has no SKILL.md", name)
		}
		pkg.Skills = append(pkg.Skills, *skill)
	}

	if raw, ok := pkg.File(MCPConfigPath); ok {
		servers, err := parseMCPConfig(raw)
		if err != nil {
			return Package{}, err
		}
		pkg.MCPServers = servers
	}

	archive, err := canonicalArchive(files)
	if err != nil {
		return Package{}, err
	}
	sum := sha256.Sum256(archive)
	pkg.Archive = archive
	pkg.SHA256 = hex.EncodeToString(sum[:])
	return pkg, nil
}

func validateFilePath(name string) error {
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, `\`) || len(name) > 1024 {
		return fmt.Errorf("invalid file path %q", name)
	}
	for _, segment := range strings.Split(name, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("invalid file path %q", name)
		}
	}
	if !filePathPattern.MatchString(name) {
		return fmt.Errorf("file path %q uses characters outside letters, digits, space, . _ -", name)
	}
	return nil
}

// manifestFieldsRefused are plugin.json fields that declare executable
// components, or MCP servers outside .mcp.json where they would escape the
// URL-only check.
var manifestFieldsRefused = []string{"hooks", "commands", "mcpServers", "lspServers"}

func parseManifest(raw []byte) (PluginManifest, []byte, error) {
	if len(raw) > maxManifestBytes {
		return PluginManifest{}, nil, fmt.Errorf("%s exceeds %d bytes", ManifestPath, maxManifestBytes)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return PluginManifest{}, nil, fmt.Errorf("%s is not a JSON object: %w", ManifestPath, err)
	}
	for _, field := range manifestFieldsRefused {
		if _, ok := object[field]; ok {
			return PluginManifest{}, nil, fmt.Errorf("%s: %q is not allowed in registry packages", ManifestPath, field)
		}
	}
	var manifest PluginManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return PluginManifest{}, nil, fmt.Errorf("%s: %w", ManifestPath, err)
	}
	if !packageNamePattern.MatchString(manifest.Name) {
		return PluginManifest{}, nil, fmt.Errorf("%s: name must be 1-128 lowercase letters, digits, . _ - starting with a letter or digit", ManifestPath)
	}
	if !semverPattern.MatchString(manifest.Version) || len(manifest.Version) > 64 {
		return PluginManifest{}, nil, fmt.Errorf("%s: version must be semver", ManifestPath)
	}
	if len(manifest.Description) > 2000 || len(manifest.DisplayName) > 160 {
		return PluginManifest{}, nil, fmt.Errorf("%s: displayName or description is too long", ManifestPath)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return PluginManifest{}, nil, fmt.Errorf("%s: %w", ManifestPath, err)
	}
	return manifest, compact.Bytes(), nil
}

// parseSkill checks the Agent Skills frontmatter: name equal to the
// directory, description present.
func parseSkill(skill *Skill, content []byte) error {
	file := "skills/" + skill.Name + "/SKILL.md"
	if !skillNamePattern.MatchString(skill.Name) || len(skill.Name) > maxSkillNameLen {
		return fmt.Errorf("%s: skill directory names use lowercase letters, digits and hyphens (max %d)", file, maxSkillNameLen)
	}
	if !utf8.Valid(content) {
		return fmt.Errorf("%s is not UTF-8 text", file)
	}
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		return fmt.Errorf("%s must start with YAML frontmatter", file)
	}
	front, _, ok := strings.Cut(rest, "\n---")
	if !ok {
		return fmt.Errorf("%s frontmatter is not closed", file)
	}
	fields := frontmatterFields(front)
	if fields["name"] != skill.Name {
		return fmt.Errorf("%s: frontmatter name %q must equal the directory name %q", file, fields["name"], skill.Name)
	}
	description := fields["description"]
	if description == "" {
		return fmt.Errorf("%s: frontmatter description is required", file)
	}
	if len(description) > maxSkillDescLen {
		return fmt.Errorf("%s: description exceeds %d characters", file, maxSkillDescLen)
	}
	skill.Description = description
	return nil
}

// frontmatterFields reads top-level scalar keys, including folded or literal
// block values, which is all the two required fields need.
func frontmatterFields(front string) map[string]string {
	fields := map[string]string{}
	lines := strings.Split(front, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '#' {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" || value == ">" || value == "|" || value == ">-" || value == "|-" {
			var block []string
			for i+1 < len(lines) && (strings.HasPrefix(lines[i+1], " ") || strings.HasPrefix(lines[i+1], "\t")) {
				i++
				block = append(block, strings.TrimSpace(lines[i]))
			}
			value = strings.Join(block, " ")
		}
		fields[strings.TrimSpace(key)] = strings.Trim(value, `"'`)
	}
	return fields
}

// parseMCPConfig accepts both the {"mcpServers": {...}} form and a bare map.
// Only remote (URL) servers are allowed, without inline credentials: the
// consumer holds secrets, never the package.
func parseMCPConfig(raw []byte) ([]MCPServer, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, fmt.Errorf("%s is not a JSON object: %w", MCPConfigPath, err)
	}
	if nested, ok := top["mcpServers"]; ok {
		top = nil
		if err := json.Unmarshal(nested, &top); err != nil {
			return nil, fmt.Errorf("%s: mcpServers must be an object: %w", MCPConfigPath, err)
		}
	}
	names := make([]string, 0, len(top))
	for name := range top {
		names = append(names, name)
	}
	sort.Strings(names)
	servers := make([]MCPServer, 0, len(names))
	for _, name := range names {
		var server map[string]any
		if err := json.Unmarshal(top[name], &server); err != nil {
			return nil, fmt.Errorf("%s: server %q must be an object", MCPConfigPath, name)
		}
		for _, refused := range []string{"command", "args", "env", "headers"} {
			if _, ok := server[refused]; ok {
				return nil, fmt.Errorf("%s: server %q declares %q; only URL servers without inline credentials are allowed", MCPConfigPath, name, refused)
			}
		}
		url, _ := server["url"].(string)
		if !strings.HasPrefix(url, "https://") {
			return nil, fmt.Errorf("%s: server %q needs an https url", MCPConfigPath, name)
		}
		kind, _ := server["type"].(string)
		if kind == "" {
			kind = "http"
		}
		if kind != "http" && kind != "sse" {
			return nil, fmt.Errorf("%s: server %q has unsupported type %q", MCPConfigPath, name, kind)
		}
		description, _ := server["description"].(string)
		servers = append(servers, MCPServer{Name: name, Type: kind, URL: url, Description: description})
	}
	return servers, nil
}

// canonicalArchive zips the files at the archive root in path order with a
// fixed timestamp, so the same files always produce the same bytes and sha256.
func canonicalArchive(files []File) ([]byte, error) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	modified := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, file := range files {
		header := &zip.FileHeader{Name: file.Path, Method: zip.Deflate, Modified: modified}
		header.SetMode(0o644)
		entry, err := writer.CreateHeader(header)
		if err != nil {
			return nil, fmt.Errorf("build archive: %w", err)
		}
		if _, err := entry.Write(file.Content); err != nil {
			return nil, fmt.Errorf("build archive: %w", err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("build archive: %w", err)
	}
	return buffer.Bytes(), nil
}
