// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package objstore

import (
	"crypto/rand"
	"encoding/base64"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/golusoris/golusoris/internal/testimages"
)

const (
	azuritePort    = "10000/tcp"
	azuriteAccount = "golusoris"
)

// AzureServer is a running Azurite blob endpoint. It verifies shared-key and
// SAS signatures, SAS expiry, and permissions; it does not create containers.
type AzureServer struct {
	// ServiceURL is the path-style account endpoint, ending in "/".
	ServiceURL  string
	AccountName string
	AccountKey  string
}

// StartAzurite boots Azurite's blob service with a per-test random account
// key, so no shared emulator secret exists in the tree.
func StartAzurite(t *testing.T) AzureServer {
	t.Helper()
	key := make([]byte, 64)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("testutil/objstore: azurite account key: %v", err)
	}
	accountKey := base64.StdEncoding.EncodeToString(key)
	hostPort := startContainer(t, "azurite", testcontainers.ContainerRequest{
		Image:        testimages.Azurite,
		ExposedPorts: []string{azuritePort},
		Env:          map[string]string{"AZURITE_ACCOUNTS": azuriteAccount + ":" + accountKey},
		Cmd: []string{
			"azurite-blob", "--blobHost", "0.0.0.0", "--blobPort", "10000",
			"--inMemoryPersistence", "--extentMemoryLimit", "512",
			"--disableTelemetry", "--disableProductStyleUrl",
			// The SDK speaks a newer service version than Azurite advertises.
			"--skipApiVersionCheck",
		},
		WaitingFor: wait.ForLog("Azurite Blob service successfully listens on"),
	}, azuritePort)
	return AzureServer{
		ServiceURL:  "http://" + hostPort + "/" + azuriteAccount + "/",
		AccountName: azuriteAccount,
		AccountKey:  accountKey,
	}
}
