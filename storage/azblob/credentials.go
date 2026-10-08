// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package azblob

import (
	"fmt"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/service"
)

// federatedTokenEnv is set by the AKS workload identity webhook.
const federatedTokenEnv = "AZURE_FEDERATED_TOKEN_FILE"

func newServiceClient(opts Options) (*service.Client, *service.SharedKeyCredential, error) {
	if opts.AccountKey != "" {
		cred, err := service.NewSharedKeyCredential(opts.AccountName, opts.AccountKey)
		if err != nil {
			return nil, nil, fmt.Errorf("storage/azblob: shared key: %w", err)
		}
		svc, err := service.NewClientWithSharedKeyCredential(opts.ServiceURL, cred, nil)
		if err != nil {
			return nil, nil, fmt.Errorf("storage/azblob: new client: %w", err)
		}
		return svc, cred, nil
	}
	cred, err := tokenCredential(opts)
	if err != nil {
		return nil, nil, err
	}
	svc, err := service.NewClient(opts.ServiceURL, cred, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("storage/azblob: new client: %w", err)
	}
	return svc, nil, nil
}

// tokenCredential is the production chain of azidentity's "prod" selection
// minus client secrets: workload identity when a federated token file is
// configured (or injected), then managed identity. Developer CLI credentials
// are never consulted.
func tokenCredential(opts Options) (azcore.TokenCredential, error) {
	sources := make([]azcore.TokenCredential, 0, 2)
	if opts.FederatedTokenFile != "" || os.Getenv(federatedTokenEnv) != "" {
		wi, err := azidentity.NewWorkloadIdentityCredential(&azidentity.WorkloadIdentityCredentialOptions{
			ClientID: opts.ClientID, TenantID: opts.TenantID, TokenFilePath: opts.FederatedTokenFile,
		})
		if err != nil {
			return nil, fmt.Errorf("storage/azblob: workload identity: %w", err)
		}
		sources = append(sources, wi)
	}
	miOpts := &azidentity.ManagedIdentityCredentialOptions{}
	if opts.ClientID != "" {
		miOpts.ID = azidentity.ClientID(opts.ClientID)
	}
	mi, err := azidentity.NewManagedIdentityCredential(miOpts)
	if err != nil {
		return nil, fmt.Errorf("storage/azblob: managed identity: %w", err)
	}
	sources = append(sources, mi)
	chain, err := azidentity.NewChainedTokenCredential(sources, nil)
	if err != nil {
		return nil, fmt.Errorf("storage/azblob: credential chain: %w", err)
	}
	return chain, nil
}
