// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build ignore
// +build ignore

// Activate: remove the go:build ignore line above, then:
//   go get github.com/otiai10/gosseract/v2
//   go mod tidy

package ocr

import (
	"context"
	"errors"
	"fmt"

	"github.com/otiai10/gosseract/v2"
)

func wrapf(format string, a ...any) error { return fmt.Errorf("ocr: "+format, a...) }

// closeOnErr releases a half-built client and joins any close failure onto err.
func closeOnErr(c *gosseract.Client, err error) error {
	if cerr := c.Close(); cerr != nil {
		return errors.Join(err, wrapf("close client: %w", cerr))
	}
	return err
}

type gosseractReader struct {
	client *gosseract.Client
}

// NewReader returns a Tesseract-backed Reader.
func NewReader(opts Options) (Reader, error) {
	c := gosseract.NewClient()
	lang := opts.Language
	if lang == "" {
		lang = "eng"
	}
	if err := c.SetLanguage(lang); err != nil {
		return nil, closeOnErr(c, wrapf("set language: %w", err))
	}
	if opts.TessdataPrefix != "" {
		c.TessdataPrefix = opts.TessdataPrefix
	}
	if opts.AllowList != "" {
		if err := c.SetWhitelist(opts.AllowList); err != nil {
			return nil, closeOnErr(c, wrapf("set whitelist: %w", err))
		}
	}
	return &gosseractReader{client: c}, nil
}

func (r *gosseractReader) Read(_ context.Context, src []byte) (string, error) {
	if err := r.client.SetImageFromBytes(src); err != nil {
		return "", wrapf("set image: %w", err)
	}
	text, err := r.client.Text()
	if err != nil {
		return "", wrapf("text: %w", err)
	}
	return text, nil
}

func (r *gosseractReader) ReadFile(_ context.Context, path string) (string, error) {
	if err := r.client.SetImage(path); err != nil {
		return "", wrapf("set image file: %w", err)
	}
	text, err := r.client.Text()
	if err != nil {
		return "", wrapf("text: %w", err)
	}
	return text, nil
}

func (r *gosseractReader) Close() error { return r.client.Close() }
