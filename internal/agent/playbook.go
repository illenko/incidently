package agent

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Playbook struct {
	Name        string
	Description string
	Tags        []string
	Content     string
}

type playbookFrontmatter struct {
	Description string   `yaml:"description"`
	Tags        []string `yaml:"tags"`
}

func LoadPlaybooks(dir string) ([]Playbook, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading playbooks directory: %w", err)
	}

	slog.Info("loading playbooks", "dir", dir)

	var playbooks []Playbook
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("reading playbook %s: %w", entry.Name(), err)
		}

		pb, err := parsePlaybook(entry.Name(), string(data))
		if err != nil {
			return nil, fmt.Errorf("parsing playbook %s: %w", entry.Name(), err)
		}

		slog.Info("playbook loaded", "name", pb.Name, "description", pb.Description, "tags", pb.Tags)
		playbooks = append(playbooks, pb)
	}

	return playbooks, nil
}

func parsePlaybook(filename, raw string) (Playbook, error) {
	name := strings.TrimSuffix(filename, ".md")

	const delimiter = "---"
	content := raw

	var fm playbookFrontmatter
	if strings.HasPrefix(strings.TrimSpace(raw), delimiter) {
		trimmed := strings.TrimSpace(raw)
		rest := trimmed[len(delimiter):]
		end := strings.Index(rest, delimiter)
		if end != -1 {
			frontmatterRaw := rest[:end]
			content = strings.TrimSpace(rest[end+len(delimiter):])

			if err := yaml.Unmarshal([]byte(frontmatterRaw), &fm); err != nil {
				return Playbook{}, fmt.Errorf("parsing frontmatter: %w", err)
			}
		}
	}

	return Playbook{
		Name:        name,
		Description: fm.Description,
		Tags:        fm.Tags,
		Content:     content,
	}, nil
}

func LoadInstruction(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading instruction file: %w", err)
	}
	return string(data), nil
}

func BuildPlaybookIndex(playbooks []Playbook) string {
	var b strings.Builder
	b.WriteString("Available playbooks:\n\n")
	for _, pb := range playbooks {
		b.WriteString(fmt.Sprintf("- **%s**: %s", pb.Name, pb.Description))
		if len(pb.Tags) > 0 {
			b.WriteString(fmt.Sprintf(" [tags: %s]", strings.Join(pb.Tags, ", ")))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func GetPlaybookByName(playbooks []Playbook, name string) (string, error) {
	for _, pb := range playbooks {
		if pb.Name == name {
			return pb.Content, nil
		}
	}
	return "", fmt.Errorf("playbook %q not found", name)
}
