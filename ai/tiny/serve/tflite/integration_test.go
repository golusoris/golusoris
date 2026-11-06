// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build integration

package tflite_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/ai/tiny"
	"github.com/golusoris/golusoris/ai/tiny/serve/tflite"
)

// maxIntegrationLabels bounds the env-supplied label list so the loops below have a scalar upper bound (HISS-02).
const maxIntegrationLabels = 16

func TestIntegration_LiveSidecar_LoadAndPredict(t *testing.T) {
	labels := strings.Split(requiredIntegrationEnv(t, "TFLITE_LABELS"), ",")
	cases := strings.Split(requiredIntegrationEnv(t, "TFLITE_EXPECT"), ",")
	require.GreaterOrEqual(t, len(labels), 2, "TFLITE_LABELS requires at least two labels")
	require.LessOrEqual(t, len(labels), maxIntegrationLabels, "TFLITE_LABELS exceeds maxIntegrationLabels")
	require.Len(t, cases, len(labels), "TFLITE_EXPECT requires one case per label")

	endpoint := os.Getenv("TFLITE_SIDECAR_ENDPOINT")
	if endpoint == "" {
		endpoint = tflite.DefaultEndpoint
	}
	p := tflite.NewPredictor(tflite.Options{Endpoint: endpoint})
	t.Cleanup(func() { require.NoError(t, p.Close()) })
	require.NoError(t, p.Load(t.Context(), integrationModel(t, labels)), "p.Load failed")

	expectedLabels := make(map[string]bool, len(labels))
	for _, entry := range cases {
		expected, imagePath, ok := strings.Cut(entry, "=")
		require.True(t, ok && imagePath != "", "TFLITE_EXPECT requires label=path cases")
		require.Contains(t, labels, expected, "TFLITE_EXPECT label missing from TFLITE_LABELS")
		require.False(t, expectedLabels[expected], "TFLITE_EXPECT repeats a label")
		expectedLabels[expected] = true
		t.Run(expected, func(t *testing.T) {
			imgData, err := os.ReadFile(imagePath)
			require.NoError(t, err)
			pred, err := p.Predict(t.Context(), imgData)
			require.NoError(t, err, "p.Predict failed")
			assertIntegrationScores(t, pred, labels)
			t.Logf("expected=%s top=%s score=%.6f", expected, pred.Labels[0].Label, pred.Labels[0].Score)
			require.Equal(t, expected, pred.Labels[0].Label, "deterministic label mapping")
			require.GreaterOrEqual(t, pred.Labels[0].Score, float32(0.6))
		})
	}
}

func integrationModel(t *testing.T, labels []string) tiny.Model {
	t.Helper()
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

	return tiny.Model{
		Name:     "colour-classifier",
		URI:      modelURI,
		Format:   tiny.FormatTFLite,
		Modality: tiny.ModalityImage,
		TaskKind: tiny.TaskClassify,
		Labels:   labels,
		Metadata: map[string]string{"sha256": sha},
	}
}

func requiredIntegrationEnv(t *testing.T, name string) string {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		t.Fatalf("%s must be set", name)
	}
	return value
}

func assertIntegrationScores(t *testing.T, pred tiny.Prediction, labels []string) {
	t.Helper()
	require.Len(t, pred.Labels, len(labels))

	var total float32
	seen := make(map[string]bool, len(labels))
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
