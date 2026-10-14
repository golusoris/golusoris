// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package plot_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/science/plot"
)

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("positive title and labels", func(t *testing.T) {
		t.Parallel()
		c, err := plot.New("Test Title", "X-Axis", "Y-Axis")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c == nil {
			t.Fatal("expected non-nil chart")
		}
		raw := c.Raw()
		if raw.Title.Text != "Test Title" {
			t.Fatalf("title = %q, want %q", raw.Title.Text, "Test Title")
		}
		if raw.X.Label.Text != "X-Axis" {
			t.Fatalf("x label = %q, want %q", raw.X.Label.Text, "X-Axis")
		}
		if raw.Y.Label.Text != "Y-Axis" {
			t.Fatalf("y label = %q, want %q", raw.Y.Label.Text, "Y-Axis")
		}
	})

	t.Run("boundary empty labels", func(t *testing.T) {
		t.Parallel()
		c, err := plot.New("", "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		raw := c.Raw()
		if raw.Title.Text != "" || raw.X.Label.Text != "" || raw.Y.Label.Text != "" {
			t.Fatal("expected empty strings on chart")
		}
	})
}

func TestAddLine(t *testing.T) {
	t.Parallel()

	t.Run("positive add line series", func(t *testing.T) {
		t.Parallel()
		c, err := plot.New("Line Chart", "X", "Y")
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		err = c.AddLine("series1", []float64{1.0, 2.0, 3.0}, []float64{10.0, 20.0, 30.0})
		if err != nil {
			t.Fatalf("AddLine() error = %v", err)
		}
	})

	t.Run("negative length mismatch", func(t *testing.T) {
		t.Parallel()
		c, err := plot.New("Line Chart", "X", "Y")
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		err = c.AddLine("bad", []float64{1.0, 2.0}, []float64{10.0})
		if err == nil {
			t.Fatal("expected length mismatch error, got nil")
		}
		if !strings.Contains(err.Error(), "xs and ys must have equal length") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})

	t.Run("boundary empty series", func(t *testing.T) {
		t.Parallel()
		c, err := plot.New("Line Chart", "X", "Y")
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		err = c.AddLine("empty", []float64{}, []float64{})
		if err != nil {
			t.Fatalf("unexpected error on empty series: %v", err)
		}
	})
}

func TestAddScatter(t *testing.T) {
	t.Parallel()

	t.Run("positive add scatter series", func(t *testing.T) {
		t.Parallel()
		c, err := plot.New("Scatter Chart", "X", "Y")
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		err = c.AddScatter("points", []float64{1.0, 2.0}, []float64{5.0, 10.0})
		if err != nil {
			t.Fatalf("AddScatter() error = %v", err)
		}
	})

	t.Run("negative length mismatch", func(t *testing.T) {
		t.Parallel()
		c, err := plot.New("Scatter Chart", "X", "Y")
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		err = c.AddScatter("bad", []float64{1.0}, []float64{1.0, 2.0})
		if err == nil {
			t.Fatal("expected length mismatch error, got nil")
		}
		if !strings.Contains(err.Error(), "xs and ys must have equal length") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})

	t.Run("boundary empty scatter series", func(t *testing.T) {
		t.Parallel()
		c, err := plot.New("Scatter Chart", "X", "Y")
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		err = c.AddScatter("empty", []float64{}, []float64{})
		if err != nil {
			t.Fatalf("unexpected error on empty scatter series: %v", err)
		}
	})
}

func TestWritePNGAndSavePNG(t *testing.T) {
	t.Parallel()
	pngMagic := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}

	t.Run("positive write png buffer", func(t *testing.T) {
		t.Parallel()
		c, err := plot.New("Render Chart", "X", "Y")
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		err = c.AddLine("line", []float64{0, 1}, []float64{0, 1})
		if err != nil {
			t.Fatalf("AddLine() error = %v", err)
		}
		var buf bytes.Buffer
		err = c.WritePNG(&buf, 200, 150)
		if err != nil {
			t.Fatalf("WritePNG() error = %v", err)
		}
		if !bytes.HasPrefix(buf.Bytes(), pngMagic) {
			t.Fatal("expected PNG magic bytes prefix")
		}
	})

	t.Run("positive save png file", func(t *testing.T) {
		t.Parallel()
		c, err := plot.New("Render Chart", "X", "Y")
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		err = c.AddLine("line", []float64{0, 1}, []float64{0, 1})
		if err != nil {
			t.Fatalf("AddLine() error = %v", err)
		}
		outPath := filepath.Join(t.TempDir(), "chart.png")
		err = c.SavePNG(outPath, 200, 150)
		if err != nil {
			t.Fatalf("SavePNG() error = %v", err)
		}
		data, err := os.ReadFile(outPath)
		if err != nil {
			t.Fatalf("reading saved file: %v", err)
		}
		if !bytes.HasPrefix(data, pngMagic) {
			t.Fatal("expected PNG magic bytes prefix in saved file")
		}
	})

	t.Run("negative save to invalid path", func(t *testing.T) {
		t.Parallel()
		c, err := plot.New("Render Chart", "X", "Y")
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		badPath := filepath.Join(t.TempDir(), "missing_dir", "sub", "chart.png")
		err = c.SavePNG(badPath, 200, 150)
		if err == nil {
			t.Fatal("expected error saving to non-existent directory")
		}
	})
}
