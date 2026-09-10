package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	sqlite "modernc.org/sqlite"
)

// backupSource is the driver's online backup entry point, reached through
// sql.Conn.Raw. The signature matches (*sqlite.Conn).NewBackup exactly.
type backupSource interface {
	NewBackup(dstURI string) (*sqlite.Backup, error)
}

// backupStepPages bounds one backup step so the copy observes context
// cancellation between steps.
const backupStepPages = 64

// Backup streams a consistent image of the database to w using the SQLite
// online backup API. The image is produced by the database engine itself,
// including content still held in the write-ahead log; copying the live main
// database file is not a backup and never happens here. Backup serializes
// against writes, so the image reflects one committed state of the
// database. Restore is owned by the installation scope under exclusive
// maintenance ownership; this method only produces the image.
func (d *database) Backup(ctx context.Context, w io.Writer) error {
	if d.closed.Load() {
		return closedFault()
	}
	if w == nil {
		return fmt.Errorf("storage: backup requires a writer")
	}
	d.wmu.Lock()
	defer d.wmu.Unlock()

	dstPath, cleanup, err := backupDestination(d.path)
	if err != nil {
		return err
	}
	defer cleanup()

	conn, err := d.db.Conn(ctx)
	if err != nil {
		return storageFault("acquire connection", err)
	}
	defer func() { _ = conn.Close() }()

	if err := conn.Raw(func(raw any) error {
		src, ok := raw.(backupSource)
		if !ok {
			return fmt.Errorf("driver connection does not support the online backup API")
		}
		return copyDatabase(ctx, src, dstPath)
	}); err != nil {
		return fmt.Errorf("storage: online backup: %w", err)
	}

	f, err := os.Open(dstPath)
	if err != nil {
		return fmt.Errorf("storage: open backup image: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := io.Copy(w, f); err != nil {
		return fmt.Errorf("storage: stream backup image: %w", err)
	}
	return nil
}

// copyDatabase drives the online backup from src into the file at dstPath.
// The source database is quiescent: Backup holds the writer lock, so a page
// step never races an in-database write.
func copyDatabase(ctx context.Context, src backupSource, dstPath string) error {
	b, err := src.NewBackup(dstPath)
	if err != nil {
		return fmt.Errorf("start backup: %w", err)
	}
	for {
		if err := ctx.Err(); err != nil {
			_ = b.Finish()
			return err
		}
		more, err := b.Step(backupStepPages)
		if err != nil {
			_ = b.Finish()
			return fmt.Errorf("copy pages: %w", err)
		}
		if !more {
			break
		}
	}
	if err := b.Finish(); err != nil {
		return fmt.Errorf("finish backup: %w", err)
	}
	return nil
}

// backupDestination creates a temporary directory for the backup image,
// preferring the database's own volume and falling back to the system
// temporary directory. The returned cleanup removes the directory.
func backupDestination(dbPath string) (string, func(), error) {
	var lastErr error
	dirs := []string{filepath.Dir(dbPath), ""}
	for _, dir := range dirs {
		tmp, err := os.MkdirTemp(dir, "zatiti-backup-")
		if err != nil {
			lastErr = err
			continue
		}
		return filepath.Join(tmp, "backup.db"), func() { _ = os.RemoveAll(tmp) }, nil
	}
	return "", nil, fmt.Errorf("storage: create backup directory: %w", lastErr)
}
