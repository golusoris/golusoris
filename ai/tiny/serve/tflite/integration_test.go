// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build integration

package tflite_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/ai/tiny"
	"github.com/golusoris/golusoris/ai/tiny/serve/tflite"
)

func TestIntegration_LiveSidecar_LoadAndPredict(t *testing.T) {
	endpoint := os.Getenv("TFLITE_SIDECAR_ENDPOINT")
	if endpoint == "" {
		endpoint = tflite.DefaultEndpoint
	}
	modelURI := os.Getenv("TFLITE_MODEL_URI")
	if modelURI == "" {
		modelURI = "file:///models/model.tflite"
	}
	modelPath := os.Getenv("TFLITE_MODEL_PATH")
	var sha string
	if modelPath != "" {
		data, err := os.ReadFile(modelPath)
		require.NoError(t, err)
		sum := sha256.Sum256(data)
		sha = hex.EncodeToString(sum[:])
	} else {
		sha = os.Getenv("TFLITE_MODEL_SHA256")
	}
	require.NotEmpty(t, sha, "TFLITE_MODEL_SHA256 or TFLITE_MODEL_PATH must be set")

	imagePath := os.Getenv("TFLITE_IMAGE_PATH")
	require.NotEmpty(t, imagePath, "TFLITE_IMAGE_PATH must be set")
	imgData, err := os.ReadFile(imagePath)
	require.NoError(t, err)

	p := tflite.NewPredictor(tflite.Options{Endpoint: endpoint})
	t.Cleanup(func() { _ = p.Close() })

	labels := []string{"cat", "dog"}
	model := tiny.Model{
		Name:     "pet-classifier",
		URI:      modelURI,
		Format:   tiny.FormatTFLite,
		Modality: tiny.ModalityImage,
		TaskKind: tiny.TaskClassify,
		Labels:   labels,
		Metadata: map[string]string{"sha256": sha},
	}

	ctx := t.Context()
	require.NoError(t, p.Load(ctx, model), "p.Load failed")

	pred, err := p.Predict(ctx, imgData)
	require.NoError(t, err, "p.Predict failed")
	require.Len(t, pred.Labels, len(labels))

	var total float32
	seen := make(map[string]bool)
	for _, l := range pred.Labels {
		require.Contains(t, labels, l.Label)
		require.False(t, seen[l.Label])
		seen[l.Label] = true
		require.GreaterOrEqual(t, l.Score, float32(0.0))
		require.LessOrEqual(t, l.Score, float32(1.0))
		total += l.Score
	}
	require.InDelta(t, 1.0, total, 0.01)
}
