// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	gerr "github.com/golusoris/golusoris/core/errors"
	"github.com/golusoris/golusoris/internal/dirsync"
)

const (
	localTransactionSchema   = 1
	maxLocalTransactionBytes = 16 * 1024
	localRecoveryTimeout     = time.Minute
	maxLocalOrphanStages     = 16
)

type localTransaction struct {
	Schema         int    `json:"schema"`
	Key            string `json:"key"`
	ObjectStage    string `json:"object_stage"`
	MetadataStage  string `json:"metadata_stage"`
	BodySize       int64  `json:"body_size"`
	BodySHA256     string `json:"body_sha256"`
	MetadataSHA256 string `json:"metadata_sha256"`
	HadObject      bool   `json:"had_object"`
	HadMetadata    bool   `json:"had_metadata"`
	ObjectBackup   string `json:"object_backup"`
	MetadataBackup string `json:"metadata_backup"`
	ObjectTarget   string `json:"object_target"`
	MetadataTarget string `json:"metadata_target"`
}

func newLocalTransaction(
	key, objectTarget, objectStage, metadataStage string,
	bodySize int64,
	bodySHA256 string,
	metadata []byte,
	hadObject, hadMetadata bool,
) localTransaction {
	metadataDigest := sha256.Sum256(metadata)
	return localTransaction{
		Schema:         localTransactionSchema,
		Key:            key,
		ObjectStage:    objectStage,
		MetadataStage:  metadataStage,
		BodySize:       bodySize,
		BodySHA256:     bodySHA256,
		MetadataSHA256: hex.EncodeToString(metadataDigest[:]),
		HadObject:      hadObject,
		HadMetadata:    hadMetadata,
		ObjectBackup:   localBackupPath(key, objectTarget, "object"),
		MetadataBackup: localBackupPath(key, objectTarget, "metadata"),
		ObjectTarget:   objectTarget,
		MetadataTarget: localMetadataPath(key, objectTarget),
	}
}

func localBackupPath(key, objectName, kind string) string {
	digest := sha256.Sum256([]byte(key))
	name := fmt.Sprintf("%sbackup-%s-%x%s", localTempPrefix, kind, digest, localTempSuffix)
	return filepath.Join(filepath.Dir(objectName), name)
}

func beginLocalTransaction(ctx context.Context, root *os.Root, tx localTransaction) (bool, error) {
	if err := checkLocalContext(ctx, "put before transaction journal"); err != nil {
		return false, err
	}
	data, err := json.Marshal(tx)
	if err != nil {
		return false, fmt.Errorf("storage: encode local transaction: %w", err)
	}
	if len(data) > maxLocalTransactionBytes {
		return false, fmt.Errorf(
			"storage: local transaction uses %d bytes, maximum is %d", len(data), maxLocalTransactionBytes,
		)
	}
	if _, err = root.Lstat(localTransactionName); !errors.Is(err, fs.ErrNotExist) {
		if err == nil {
			return false, errors.New("storage: pending local transaction was not recovered")
		}
		return false, fmt.Errorf("storage: inspect local transaction: %w", err)
	}
	stage, err := stageLocalTransaction(ctx, root, data)
	if err != nil {
		return false, err
	}
	if err = root.Rename(stage, localTransactionName); err != nil {
		published, inspectErr := localPathExists(root, localTransactionName)
		if published {
			return true, fmt.Errorf("storage: publish local transaction: %w", err)
		}
		return false, errors.Join(
			fmt.Errorf("storage: publish local transaction: %w", err), inspectErr, removeLocalTemp(root, stage),
		)
	}
	if err = syncLocalParent(root, "."); err != nil {
		return true, fmt.Errorf("storage: sync local transaction journal: %w", err)
	}
	return true, nil
}

func stageLocalTransaction(ctx context.Context, root *os.Root, data []byte) (string, error) {
	file, name, err := openLocalTemp(root)
	if err != nil {
		return "", err
	}
	if _, err = file.Write(data); err != nil {
		return "", closeAndRemoveLocalTemp(root, file, name, fmt.Errorf("storage: stage local transaction: %w", err))
	}
	if err = checkLocalContext(ctx, "put after transaction write"); err != nil {
		return "", closeAndRemoveLocalTemp(root, file, name, err)
	}
	if err = file.Sync(); err != nil {
		return "", closeAndRemoveLocalTemp(root, file, name, fmt.Errorf("storage: sync local transaction: %w", err))
	}
	if err = file.Close(); err != nil {
		return "", errors.Join(fmt.Errorf("storage: close local transaction: %w", err), removeLocalTemp(root, name))
	}
	return name, nil
}

func recoverPendingLocalTransaction(ctx context.Context, root *os.Root) error {
	tx, found, err := readLocalTransaction(ctx, root)
	if err != nil || !found {
		return err
	}
	committed, err := localTransactionCommitted(ctx, root, tx)
	if err != nil {
		return err
	}
	if committed {
		return finishLocalTransaction(root, tx)
	}
	if err = rollbackLocalTransaction(ctx, root, tx); err != nil {
		return err
	}
	return finishLocalTransaction(root, tx)
}

func reclaimLocalOrphanStages(ctx context.Context, root *os.Root) (err error) {
	if err = checkLocalContext(ctx, "orphan-stage recovery preflight"); err != nil {
		return err
	}
	protected, err := protectedLocalTransactionStages(ctx, root)
	if err != nil {
		return err
	}
	directory, err := openLocalStagingDirectory(root)
	if err != nil {
		return err
	}
	defer gerr.CloseInto(directory, &err, "storage: close local staging directory")
	orphans, err := collectLocalOrphanStages(ctx, root, directory, protected)
	if err != nil {
		return err
	}
	if err = removeLocalOrphanStages(ctx, root, orphans); err != nil || len(orphans) == 0 {
		return err
	}
	if err = dirsync.Sync(directory); err != nil {
		return fmt.Errorf("storage: sync reclaimed staging directory: %w", err)
	}
	return nil
}

func collectLocalOrphanStages(
	ctx context.Context,
	root *os.Root,
	directory *os.File,
	protected map[string]struct{},
) ([]localOrphanStage, error) {
	entries, err := directory.ReadDir(maxLocalOrphanStages + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("storage: read local staging directory: %w", err)
	}
	if len(entries) > maxLocalOrphanStages {
		return nil, fmt.Errorf(
			"storage: local staging directory exceeds %d entries", maxLocalOrphanStages,
		)
	}
	orphans := make([]localOrphanStage, 0, len(entries))
	for _, entry := range entries {
		if err = checkLocalContext(ctx, "orphan-stage recovery scan"); err != nil {
			return nil, err
		}
		stage, inspectErr := inspectLocalStageEntry(root, entry.Name())
		if inspectErr != nil {
			return nil, inspectErr
		}
		if _, keep := protected[stage.name]; !keep {
			orphans = append(orphans, stage)
		}
	}
	return orphans, nil
}

func inspectLocalStageEntry(root *os.Root, name string) (localOrphanStage, error) {
	if !isLocalTempName(name) {
		return localOrphanStage{}, fmt.Errorf("storage: unexpected local staging entry %q", name)
	}
	stage := filepath.Join(localStagingDirectoryName, name)
	info, err := root.Lstat(stage)
	if err != nil {
		return localOrphanStage{}, fmt.Errorf("storage: inspect orphan stage %q: %w", stage, err)
	}
	if !info.Mode().IsRegular() {
		return localOrphanStage{}, fmt.Errorf("storage: orphan stage %q must be a regular file", stage)
	}
	return localOrphanStage{name: stage, info: info}, nil
}

func removeLocalOrphanStages(
	ctx context.Context, root *os.Root, orphans []localOrphanStage,
) error {
	for _, orphan := range orphans {
		if err := checkLocalContext(ctx, "orphan-stage recovery remove"); err != nil {
			return err
		}
		current, inspectErr := root.Lstat(orphan.name)
		if inspectErr != nil {
			return fmt.Errorf("storage: reinspect orphan stage %q: %w", orphan.name, inspectErr)
		}
		if !current.Mode().IsRegular() || !os.SameFile(orphan.info, current) {
			return fmt.Errorf("storage: orphan stage %q changed during recovery", orphan.name)
		}
		if removeErr := root.Remove(orphan.name); removeErr != nil {
			return fmt.Errorf("storage: remove orphan stage %q: %w", orphan.name, removeErr)
		}
	}
	return nil
}

type localOrphanStage struct {
	name string
	info os.FileInfo
}

func protectedLocalTransactionStages(
	ctx context.Context, root *os.Root,
) (map[string]struct{}, error) {
	tx, found, err := readLocalTransaction(ctx, root)
	if err != nil {
		return nil, err
	}
	protected := make(map[string]struct{}, 2)
	if found {
		protected[tx.ObjectStage] = struct{}{}
		protected[tx.MetadataStage] = struct{}{}
	}
	return protected, nil
}

func openLocalStagingDirectory(root *os.Root) (*os.File, error) {
	before, err := root.Lstat(localStagingDirectoryName)
	if errors.Is(err, fs.ErrNotExist) {
		if err = root.Mkdir(localStagingDirectoryName, 0o700); err != nil {
			return nil, fmt.Errorf("storage: create local staging directory: %w", err)
		}
		if err = syncLocalParent(root, "."); err != nil {
			return nil, fmt.Errorf("storage: sync local staging directory creation: %w", err)
		}
		before, err = root.Lstat(localStagingDirectoryName)
	}
	if err != nil {
		return nil, fmt.Errorf("storage: inspect local staging directory: %w", err)
	}
	if !before.IsDir() {
		return nil, errors.New("storage: local staging path must be a directory")
	}
	directory, err := root.Open(localStagingDirectoryName)
	if err != nil {
		return nil, fmt.Errorf("storage: open local staging directory: %w", err)
	}
	opened, err := directory.Stat()
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("storage: stat local staging directory: %w", err), directory.Close(),
		)
	}
	if !opened.IsDir() || !os.SameFile(before, opened) {
		return nil, errors.Join(
			errors.New("storage: local staging directory changed during open"), directory.Close(),
		)
	}
	return directory, nil
}

func recoverLocalTransactionAfterFailure(ctx context.Context, root *os.Root) error {
	recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), localRecoveryTimeout)
	defer cancel()
	return recoverPendingLocalTransaction(recoveryCtx, root)
}

func publishLocalTransaction(root *os.Root, tx localTransaction) error {
	if err := installLocalTransactionFile(
		root, tx.MetadataStage, tx.MetadataTarget, tx.MetadataBackup, tx.HadMetadata,
	); err != nil {
		return fmt.Errorf("storage: publish local attributes: %w", err)
	}
	if err := installLocalTransactionFile(
		root, tx.ObjectStage, tx.ObjectTarget, tx.ObjectBackup, tx.HadObject,
	); err != nil {
		return fmt.Errorf("storage: publish staged object: %w", err)
	}
	return nil
}

func installLocalTransactionFile(
	root *os.Root, staged, target, backup string, hadTarget bool,
) error {
	backupExists, err := localPathExists(root, backup)
	if err != nil {
		return err
	}
	if backupExists {
		return errors.New("local transaction backup already exists")
	}
	targetExists, err := localPathExists(root, target)
	if err != nil {
		return err
	}
	if targetExists != hadTarget {
		return errors.New("local transaction target changed before publish")
	}
	if hadTarget {
		if err = root.Rename(target, backup); err != nil {
			return fmt.Errorf("move current file to backup: %w", err)
		}
	}
	if err = root.Rename(staged, target); err != nil {
		return fmt.Errorf("move staged file into place: %w", err)
	}
	return nil
}

func readLocalTransaction(ctx context.Context, root *os.Root) (localTransaction, bool, error) {
	data, found, err := readBoundedLocalFile(
		ctx, root, localTransactionName, "local transaction",
	)
	if err != nil || !found {
		return localTransaction{}, found, err
	}
	var tx localTransaction
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&tx); err != nil {
		return localTransaction{}, false, fmt.Errorf("storage: decode local transaction: %w", err)
	}
	var trailing json.RawMessage
	if err = decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return localTransaction{}, false, errors.New("storage: local transaction contains trailing JSON")
	}
	if err = validateLocalTransaction(tx); err != nil {
		return localTransaction{}, false, err
	}
	return tx, true, nil
}

func validateLocalTransaction(tx localTransaction) error {
	if err := validateLocalTransactionTarget(tx); err != nil {
		return err
	}
	if err := validateLocalTransactionFields(tx); err != nil {
		return err
	}
	return validateLocalTransactionPaths(tx)
}

func validateLocalTransactionTarget(tx localTransaction) error {
	clean, objectTarget, err := localKey(tx.Key)
	if err != nil || clean != tx.Key || objectTarget != tx.ObjectTarget {
		return errors.New("storage: local transaction contains invalid object target")
	}
	return nil
}

func validateLocalTransactionFields(tx localTransaction) error {
	if tx.Schema != localTransactionSchema || tx.BodySize < 0 ||
		!validLocalDigest(tx.BodySHA256) || !validLocalDigest(tx.MetadataSHA256) {
		return errors.New("storage: local transaction contains invalid fields")
	}
	return nil
}

func validateLocalTransactionPaths(tx localTransaction) error {
	wantMetadata := localMetadataPath(tx.Key, tx.ObjectTarget)
	wantObjectBackup := localBackupPath(tx.Key, tx.ObjectTarget, "object")
	wantMetadataBackup := localBackupPath(tx.Key, tx.ObjectTarget, "metadata")
	if tx.MetadataTarget != wantMetadata || tx.ObjectBackup != wantObjectBackup ||
		tx.MetadataBackup != wantMetadataBackup {
		return errors.New("storage: local transaction contains invalid internal paths")
	}
	if !validLocalStage(tx.ObjectStage) || !validLocalStage(tx.MetadataStage) {
		return errors.New("storage: local transaction contains invalid stage path")
	}
	return validateDistinctLocalTransactionPaths(tx)
}

func validateDistinctLocalTransactionPaths(tx localTransaction) error {
	if tx.ObjectStage == tx.MetadataStage || tx.ObjectStage == tx.ObjectBackup ||
		tx.ObjectStage == tx.MetadataBackup || tx.MetadataStage == tx.ObjectBackup ||
		tx.MetadataStage == tx.MetadataBackup {
		return errors.New("storage: local transaction reuses an internal path")
	}
	return nil
}

func validLocalStage(stage string) bool {
	return filepath.Dir(stage) == localStagingDirectoryName && isLocalTempName(filepath.Base(stage))
}

func validLocalDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func localTransactionCommitted(ctx context.Context, root *os.Root, tx localTransaction) (bool, error) {
	metadata, found, err := readBoundedLocalFile(
		ctx, root, tx.MetadataTarget, "local attributes",
	)
	if err != nil || !found {
		return false, err
	}
	metadataDigest := sha256.Sum256(metadata)
	if hex.EncodeToString(metadataDigest[:]) != tx.MetadataSHA256 {
		return false, nil
	}
	attrs, err := decodeLocalAttributes(metadata, tx.Key)
	if err != nil || attrs.BodySize != tx.BodySize || attrs.BodySHA256 != tx.BodySHA256 {
		return false, err
	}
	return localBodyMatches(ctx, root, tx.ObjectTarget, tx.BodySize, tx.BodySHA256)
}

func rollbackLocalTransaction(ctx context.Context, root *os.Root, tx localTransaction) error {
	if err := rollbackLocalBody(ctx, root, tx); err != nil {
		return err
	}
	return rollbackLocalMetadata(ctx, root, tx)
}

func rollbackLocalBody(ctx context.Context, root *os.Root, tx localTransaction) error {
	backupExists, err := localPathExists(root, tx.ObjectBackup)
	if err != nil {
		return err
	}
	if backupExists {
		return restoreLocalBackup(root, tx.ObjectTarget, tx.ObjectBackup)
	}
	matches, err := localBodyMatches(ctx, root, tx.ObjectTarget, tx.BodySize, tx.BodySHA256)
	if err != nil {
		return err
	}
	if matches && !tx.HadObject {
		_, err = removeLocalObject(root, tx.ObjectTarget)
	}
	return err
}

func rollbackLocalMetadata(ctx context.Context, root *os.Root, tx localTransaction) error {
	backupExists, err := localPathExists(root, tx.MetadataBackup)
	if err != nil {
		return err
	}
	if backupExists {
		return restoreLocalBackup(root, tx.MetadataTarget, tx.MetadataBackup)
	}
	data, found, err := readBoundedLocalFile(
		ctx, root, tx.MetadataTarget, "local attributes",
	)
	if err != nil || !found {
		return err
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) == tx.MetadataSHA256 && !tx.HadMetadata {
		_, err = removeLocalAttributes(root, tx.MetadataTarget)
	}
	return err
}

func finishLocalTransaction(root *os.Root, tx localTransaction) error {
	if err := cleanupLocalStages(
		root, tx.ObjectStage, tx.MetadataStage, tx.ObjectBackup, tx.MetadataBackup,
	); err != nil {
		return err
	}
	if err := syncLocalParent(root, filepath.Dir(tx.ObjectTarget)); err != nil {
		return fmt.Errorf("storage: sync finalized local transaction parent: %w", err)
	}
	removeErr := root.Remove(localTransactionName)
	if removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
		return fmt.Errorf("storage: remove local transaction: %w", removeErr)
	}
	if err := syncLocalParent(root, "."); err != nil {
		return fmt.Errorf("storage: sync finalized local transaction root: %w", err)
	}
	return nil
}

func restoreLocalBackup(root *os.Root, target, backup string) error {
	if err := removeLocalPath(root, target); err != nil {
		return err
	}
	if err := root.Rename(backup, target); err != nil {
		return fmt.Errorf("storage: restore local transaction backup: %w", err)
	}
	return nil
}

func removeLocalPath(root *os.Root, name string) error {
	err := root.Remove(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("storage: remove local transaction target: %w", err)
	}
	return nil
}

func localPathExists(root *os.Root, name string) (bool, error) {
	_, err := root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("storage: inspect local transaction path: %w", err)
	}
	return true, nil
}

func localRegularPathExists(root *os.Root, name, kind string) (bool, error) {
	info, err := root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("storage: inspect local %s: %w", kind, err)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("storage: local %s must be a regular file", kind)
	}
	return true, nil
}

func readBoundedLocalFile(
	ctx context.Context, root *os.Root, name string, kind string,
) (_ []byte, found bool, err error) {
	file, found, err := openBoundedLocalFile(root, name, kind)
	if err != nil || !found {
		return nil, found, err
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("storage: close %s: %w", kind, closeErr))
		}
	}()
	data, err := io.ReadAll(io.LimitReader(
		checkedReader{check: ctx.Err, src: file}, maxLocalAttributeBytes+1,
	))
	if err != nil {
		return nil, false, fmt.Errorf("storage: read %s: %w", kind, err)
	}
	if len(data) > maxLocalAttributeBytes {
		return nil, false, fmt.Errorf("storage: %s exceeds %d bytes", kind, maxLocalAttributeBytes)
	}
	return data, true, nil
}

func openBoundedLocalFile(root *os.Root, name, kind string) (*os.File, bool, error) {
	before, err := root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("storage: inspect %s: %w", kind, err)
	}
	if !before.Mode().IsRegular() {
		return nil, false, fmt.Errorf("storage: %s must be a regular file", kind)
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, false, fmt.Errorf("storage: open %s: %w", kind, err)
	}
	opened, err := file.Stat()
	if err != nil {
		return nil, false, errors.Join(fmt.Errorf("storage: stat opened %s: %w", kind, err), file.Close())
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return nil, false, errors.Join(errors.New("storage: local file changed during bounded open"), file.Close())
	}
	return file, true, nil
}
