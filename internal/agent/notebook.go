package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Jupyter notebooks are JSON with a lot of noise (outputs, base64 images), so
// Read shows their cells as text and NotebookEdit changes one cell at a time.

var notebookTool = tool{access: editsFiles, def: ToolDef{Name: "NotebookEdit",
	Description: "Replace, insert or delete a cell in a Jupyter notebook (.ipynb). Read the notebook first to see the cell ids. " +
		"Replace and delete act on cell_id; insert adds the new cell after cell_id (at the start when it is empty).",
	Schema: obj(map[string]any{
		"notebook_path": prop("string", "Path to the .ipynb file"),
		"cell_id":       prop("string", "The cell's id, or its number as \"cell-N\" (counting from 0)"),
		"new_source":    prop("string", "The cell's new content (not needed for delete)"),
		"cell_type":     map[string]any{"type": "string", "enum": []string{"code", "markdown"}, "description": "Required for insert; optional for replace"},
		"edit_mode":     map[string]any{"type": "string", "enum": []string{"replace", "insert", "delete"}, "description": "Default: replace"},
	}, "notebook_path")}, run: runNotebookEdit}

// notebookText lists a notebook's cells, without their outputs.
func notebookText(b []byte) (string, error) {
	var nb struct {
		Cells []struct {
			ID       string          `json:"id"`
			CellType string          `json:"cell_type"`
			Source   json.RawMessage `json:"source"`
		} `json:"cells"`
	}
	if err := json.Unmarshal(b, &nb); err != nil {
		return "", fmt.Errorf("not a valid notebook: %w", err)
	}
	var sb strings.Builder
	for i, c := range nb.Cells {
		id := c.ID
		if id == "" {
			id = "cell-" + strconv.Itoa(i)
		}
		fmt.Fprintf(&sb, "--- cell %d · id %s · %s ---\n%s\n", i, id, c.CellType, strings.TrimRight(cellSource(c.Source), "\n"))
	}
	if sb.Len() == 0 {
		return "(notebook has no cells)", nil
	}
	return sb.String(), nil
}

// cellSource reads a cell's source, which nbformat stores as a string or a list of lines.
func cellSource(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var lines []string
	json.Unmarshal(raw, &lines)
	return strings.Join(lines, "")
}

func sourceLines(s string) []any {
	out := []any{}
	for _, l := range strings.SplitAfter(s, "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func runNotebookEdit(_ context.Context, t *Toolbox, in map[string]any) (string, error) {
	p := t.abs(str(in, "notebook_path"))
	if !strings.HasSuffix(p, ".ipynb") {
		return "", fmt.Errorf("%s is not a .ipynb file: use Edit for other files", p)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	if !t.wasRead(p) {
		//lint:ignore ST1005 starts with the tool name
		return "", fmt.Errorf("Read %s before editing it", p)
	}
	var nb map[string]any
	if err := json.Unmarshal(b, &nb); err != nil {
		return "", fmt.Errorf("not a valid notebook: %w", err)
	}
	cells, _ := nb["cells"].([]any)

	// find is the index of the cell_id, or -1.
	find := func() (int, error) {
		id := str(in, "cell_id")
		for i, c := range cells {
			if m, _ := c.(map[string]any); m != nil && m["id"] == id && id != "" {
				return i, nil
			}
		}
		if n, ok := strings.CutPrefix(id, "cell-"); ok {
			if i, err := strconv.Atoi(n); err == nil && i >= 0 && i < len(cells) {
				return i, nil
			}
		}
		return -1, fmt.Errorf("no cell %q: Read the notebook to see its cell ids", id)
	}
	cellType := str(in, "cell_type")
	if cellType != "" && cellType != "code" && cellType != "markdown" {
		return "", fmt.Errorf("cell_type must be code or markdown")
	}

	var done string
	switch mode := str(in, "edit_mode"); mode {
	case "", "replace":
		i, err := find()
		if err != nil {
			return "", err
		}
		c, _ := cells[i].(map[string]any)
		c["source"] = sourceLines(str(in, "new_source"))
		if cellType != "" && cellType != c["cell_type"] {
			c["cell_type"] = cellType
			fixCellType(c)
		} else if c["cell_type"] == "code" {
			c["outputs"], c["execution_count"] = []any{}, nil // the old results no longer match
		}
		done = fmt.Sprintf("Replaced cell %d", i)
	case "insert":
		if cellType == "" {
			return "", fmt.Errorf("cell_type is required to insert a cell")
		}
		at := 0
		if str(in, "cell_id") != "" {
			i, err := find()
			if err != nil {
				return "", err
			}
			at = i + 1
		}
		c := map[string]any{"cell_type": cellType, "metadata": map[string]any{}, "source": sourceLines(str(in, "new_source"))}
		fixCellType(c)
		if minor, _ := nb["nbformat_minor"].(float64); minor >= 5 {
			id := make([]byte, 4)
			rand.Read(id)
			c["id"] = hex.EncodeToString(id)
		}
		cells = append(cells[:at], append([]any{c}, cells[at:]...)...)
		done = fmt.Sprintf("Inserted a %s cell at position %d", cellType, at)
	case "delete":
		i, err := find()
		if err != nil {
			return "", err
		}
		cells = append(cells[:i], cells[i+1:]...)
		done = fmt.Sprintf("Deleted cell %d", i)
	default:
		return "", fmt.Errorf("edit_mode must be replace, insert or delete")
	}
	nb["cells"] = cells

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ") // what Jupyter writes
	if err := enc.Encode(nb); err != nil {
		return "", err
	}
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		return "", err
	}
	return done + " in " + p, nil
}

// fixCellType gives a cell the fields its type needs and drops the others.
func fixCellType(c map[string]any) {
	if c["cell_type"] == "code" {
		c["outputs"], c["execution_count"] = []any{}, nil
	} else {
		delete(c, "outputs")
		delete(c, "execution_count")
	}
}
