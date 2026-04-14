// Package plot provides thin helpers over gonum/plot for generating charts.
//
// This is a separate go.mod sub-module because gonum/plot pulls font and image
// rendering libraries that add ~20 MB to the dep graph.
// Import directly: github.com/golusoris/golusoris/science/plot
package plot

import (
	"fmt"
	"io"

	gnplot "gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/vg"
	"gonum.org/v1/plot/vg/draw"
	"gonum.org/v1/plot/vg/vgimg"
)

// Chart wraps a gonum/plot.Plot for a simplified API.
type Chart struct {
	p *gnplot.Plot
}

// New creates a new Chart with title and axis labels.
func New(title, xLabel, yLabel string) (*Chart, error) {
	p := gnplot.New()
	p.Title.Text = title
	p.X.Label.Text = xLabel
	p.Y.Label.Text = yLabel
	return &Chart{p: p}, nil
}

// AddLine adds a line series from xy pairs.
func (c *Chart) AddLine(label string, xs, ys []float64) error {
	if len(xs) != len(ys) {
		return fmt.Errorf("plot: xs and ys must have equal length")
	}
	pts := make(plotter.XYs, len(xs))
	for i := range xs {
		pts[i] = plotter.XY{X: xs[i], Y: ys[i]}
	}
	l, err := plotter.NewLine(pts)
	if err != nil {
		return fmt.Errorf("plot: new line: %w", err)
	}
	c.p.Add(l)
	c.p.Legend.Add(label, l)
	return nil
}

// AddScatter adds a scatter series.
func (c *Chart) AddScatter(label string, xs, ys []float64) error {
	if len(xs) != len(ys) {
		return fmt.Errorf("plot: xs and ys must have equal length")
	}
	pts := make(plotter.XYs, len(xs))
	for i := range xs {
		pts[i] = plotter.XY{X: xs[i], Y: ys[i]}
	}
	s, err := plotter.NewScatter(pts)
	if err != nil {
		return fmt.Errorf("plot: new scatter: %w", err)
	}
	c.p.Add(s)
	c.p.Legend.Add(label, s)
	return nil
}

// WritePNG writes the chart as a PNG to w at widthPx × heightPx.
func (c *Chart) WritePNG(w io.Writer, widthPx, heightPx float64) error {
	width := vg.Length(widthPx) * vg.Inch / 96  // screen DPI 96
	height := vg.Length(heightPx) * vg.Inch / 96
	canvas := vgimg.New(width, height)
	dc := draw.New(canvas)
	c.p.Draw(dc)
	_, err := vgimg.PngCanvas{Canvas: canvas}.WriteTo(w)
	if err != nil {
		return fmt.Errorf("plot: write png: %w", err)
	}
	return nil
}

// SavePNG writes the chart to a file.
func (c *Chart) SavePNG(path string, widthPx, heightPx float64) error {
	if err := c.p.Save(
		vg.Length(widthPx)*vg.Inch/96,
		vg.Length(heightPx)*vg.Inch/96,
		path,
	); err != nil {
		return fmt.Errorf("plot: save %s: %w", path, err)
	}
	return nil
}

// Raw returns the underlying *gnplot.Plot for advanced use.
func (c *Chart) Raw() *gnplot.Plot { return c.p }
