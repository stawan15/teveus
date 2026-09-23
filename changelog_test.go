package main

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"
)

// docs/changelog.json (read by the website) is generated from CHANGELOG.md
// by docs/changelog.py; this fails when one was edited without the other.
func TestChangelogJSONMatchesMarkdown(t *testing.T) {
	md, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("docs/changelog.json")
	if err != nil {
		t.Fatal(err)
	}
	var data struct {
		Latest   string `json:"latest"`
		Versions []struct {
			Version  string `json:"version"`
			Sections []struct {
				Items []json.RawMessage `json:"items"`
			} `json:"sections"`
		} `json:"versions"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	var versions []string
	for _, m := range regexp.MustCompile(`(?m)^## \[([^\]]+)\]`).FindAllSubmatch(md, -1) {
		versions = append(versions, string(m[1]))
	}
	items := len(regexp.MustCompile(`(?m)^- `).FindAll(md, -1))
	got := 0
	for _, v := range data.Versions {
		for _, s := range v.Sections {
			got += len(s.Items)
		}
	}
	if len(versions) != len(data.Versions) || got != items {
		t.Fatalf("docs/changelog.json is out of date (%d versions, %d items in CHANGELOG.md; %d, %d in the JSON): run python3 docs/changelog.py",
			len(versions), items, len(data.Versions), got)
	}
	for i, v := range versions {
		if data.Versions[i].Version != v {
			t.Fatalf("version %d: %q in CHANGELOG.md, %q in the JSON: run python3 docs/changelog.py", i, v, data.Versions[i].Version)
		}
	}
	if len(versions) > 0 && versions[0] != "Unreleased" && data.Latest != versions[0] {
		t.Fatalf("latest = %q, want %q", data.Latest, versions[0])
	}
}
