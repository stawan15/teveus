package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleNotebook = `{
 "cells": [
  {"cell_type": "markdown", "id": "intro", "metadata": {}, "source": ["# Title\n", "text"]},
  {"cell_type": "code", "id": "calc", "metadata": {}, "execution_count": 3,
   "outputs": [{"output_type": "stream", "name": "stdout", "text": ["big output\n"]}], "source": "print(1)"}
 ],
 "metadata": {}, "nbformat": 4, "nbformat_minor": 5
}`

func TestNotebookReadAndEdit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.ipynb")
	os.WriteFile(path, []byte(sampleNotebook), 0o644)
	box := NewToolbox(dir)
	edit := func(in map[string]any) (string, error) {
		in["notebook_path"] = "a.ipynb"
		return runNotebookEdit(context.TODO(), box, in)
	}

	if _, err := edit(map[string]any{"cell_id": "calc", "new_source": "x"}); err == nil {
		t.Fatal("editing before reading should fail")
	}
	shown, _ := runRead(context.TODO(), box, map[string]any{"file_path": "a.ipynb"})
	if !strings.Contains(shown, "id calc · code") || !strings.Contains(shown, "print(1)") || strings.Contains(shown, "big output") {
		t.Fatalf("Read shows: %s", shown)
	}

	if _, err := edit(map[string]any{"cell_id": "calc", "new_source": "print(2)\nprint(3)"}); err != nil {
		t.Fatal(err)
	}
	if _, err := edit(map[string]any{"edit_mode": "insert", "cell_id": "intro", "cell_type": "code", "new_source": "y = 1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := edit(map[string]any{"edit_mode": "delete", "cell_id": "cell-0"}); err != nil {
		t.Fatal(err)
	}
	if _, err := edit(map[string]any{"cell_id": "missing", "new_source": "z"}); err == nil {
		t.Fatal("an unknown cell should fail")
	}

	b, _ := os.ReadFile(path)
	var nb struct {
		Cells []struct {
			ID      string `json:"id"`
			Type    string `json:"cell_type"`
			Source  []string
			Outputs []any `json:"outputs"`
		}
	}
	if err := json.Unmarshal(b, &nb); err != nil {
		t.Fatalf("the notebook is no longer valid JSON: %v\n%s", err, b)
	}
	if len(nb.Cells) != 2 || nb.Cells[0].Type != "code" || nb.Cells[0].Source[0] != "y = 1" || nb.Cells[0].ID == "" {
		t.Fatalf("cells = %+v", nb.Cells)
	}
	if c := nb.Cells[1]; c.ID != "calc" || strings.Join(c.Source, "") != "print(2)\nprint(3)" || len(c.Outputs) != 0 {
		t.Fatalf("replaced cell = %+v (its old output should be gone)", c)
	}
}
