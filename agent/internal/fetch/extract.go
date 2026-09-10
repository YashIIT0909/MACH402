package fetch

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// IsArchive reports whether a filename looks like something Extract can unpack.
func IsArchive(name string) bool {
	lower := strings.ToLower(name)
	for _, suffix := range []string{".tar", ".tar.gz", ".tgz", ".zip"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

// Extract unpacks an archive into destDir, refusing anything that would escape it.
//
// Archives arrive from a paying stranger, so every entry is treated as hostile:
// "../../etc/passwd" (Zip Slip), absolute paths, symlinks pointing outside the
// directory, device nodes, and archives that decompress to far more than they
// claim (a zip bomb) are all rejected rather than written.
func Extract(archivePath, destDir string, maxBytes int64) error {
	lower := strings.ToLower(archivePath)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return extractZip(archivePath, destDir, maxBytes)
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return extractTar(archivePath, destDir, maxBytes, true)
	case strings.HasSuffix(lower, ".tar"):
		return extractTar(archivePath, destDir, maxBytes, false)
	default:
		return fmt.Errorf("cannot extract %s: unsupported archive type", filepath.Base(archivePath))
	}
}

// safeJoin resolves an archive entry name inside destDir, or fails.
func safeJoin(destDir, name string) (string, error) {
	if filepath.IsAbs(name) || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("archive entry %q has an absolute path", name)
	}
	cleaned := filepath.Clean(filepath.Join(destDir, name))
	prefix := filepath.Clean(destDir) + string(os.PathSeparator)
	if cleaned != filepath.Clean(destDir) && !strings.HasPrefix(cleaned, prefix) {
		return "", fmt.Errorf("archive entry %q escapes the dataset directory", name)
	}
	return cleaned, nil
}

func extractTar(archivePath, destDir string, maxBytes int64, compressed bool) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()

	var source io.Reader = file
	if compressed {
		gz, err := gzip.NewReader(file)
		if err != nil {
			return fmt.Errorf("read gzip: %w", err)
		}
		defer gz.Close()
		source = gz
	}

	reader := tar.NewReader(source)
	var total int64

	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}

		target, err := safeJoin(destDir, header.Name)
		if err != nil {
			return err
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			total += header.Size
			if total > maxBytes {
				return fmt.Errorf("archive expands to more than %s", HumanBytes(maxBytes))
			}
			if err := writeFile(target, reader, header.Size, maxBytes); err != nil {
				return err
			}
		case tar.TypeSymlink, tar.TypeLink:
			// A symlink is how an archive reaches a file it does not contain.
			// The dataset directory is data, not a filesystem to be rebuilt.
			return fmt.Errorf("archive entry %q is a link; links are not extracted", header.Name)
		default:
			// Devices, fifos and the rest have no business in a dataset.
			continue
		}
	}
}

func extractZip(archivePath, destDir string, maxBytes int64) error {
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("read zip: %w", err)
	}
	defer archive.Close()

	var total int64
	for _, entry := range archive.File {
		target, err := safeJoin(destDir, entry.Name)
		if err != nil {
			return err
		}

		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive entry %q is a symlink; links are not extracted", entry.Name)
		}

		// UncompressedSize64 is the archive's own claim; writeFile enforces the
		// real limit as bytes arrive.
		total += int64(entry.UncompressedSize64)
		if total > maxBytes {
			return fmt.Errorf("archive expands to more than %s", HumanBytes(maxBytes))
		}

		source, err := entry.Open()
		if err != nil {
			return err
		}
		err = writeFile(target, source, int64(entry.UncompressedSize64), maxBytes)
		source.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// writeFile creates one extracted file, copying no more than the limit allows.
func writeFile(target string, source io.Reader, declared, maxBytes int64) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	// +1 so a file longer than it declared is detected rather than truncated.
	limit := declared + 1
	if limit <= 0 || limit > maxBytes {
		limit = maxBytes
	}
	written, err := io.Copy(file, io.LimitReader(source, limit))
	if err != nil {
		return fmt.Errorf("extract %s: %w", filepath.Base(target), err)
	}
	if declared >= 0 && written > declared {
		return fmt.Errorf("archive entry %s is larger than it declared", filepath.Base(target))
	}
	return nil
}
