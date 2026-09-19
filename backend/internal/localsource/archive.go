package localsource

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bodgit/sevenzip"
	rardecode "github.com/nwaples/rardecode/v2"
)

type archiveKind int

const (
	archiveZip archiveKind = iota
	archiveRar
	archiveSevenZip
	archiveTar
	archivePdf
)

func kindOf(archivePath string) archiveKind {
	switch strings.ToLower(filepath.Ext(archivePath)) {
	case ".cbr", ".rar":
		return archiveRar
	case ".cb7", ".7z":
		return archiveSevenZip
	case ".cbt", ".tar":
		return archiveTar
	case ".pdf":
		return archivePdf
	default:
		return archiveZip
	}
}

func ArchiveHasEntry(archivePath, entryName string) bool {
	switch kindOf(archivePath) {
	case archivePdf:
		names, err := pdfPageNames(archivePath)
		if err != nil {
			return false
		}
		for _, n := range names {
			if n == entryName {
				return true
			}
		}
		return false

	case archiveRar:
		rr, err := rardecode.OpenReader(archivePath)
		if err != nil {
			return false
		}
		defer rr.Close()
		for {
			h, err := rr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return false
			}
			if !h.IsDir && h.Name == entryName {
				return true
			}
		}
		return false

	case archiveSevenZip:
		zr, err := sevenzip.OpenReader(archivePath)
		if err != nil {
			return false
		}
		defer zr.Close()
		for _, f := range zr.File {
			if f.Name == entryName {
				return true
			}
		}
		return false

	case archiveTar:
		f, err := os.Open(archivePath)
		if err != nil {
			return false
		}
		defer f.Close()
		tr := tar.NewReader(f)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return false
			}
			if h.Typeflag == tar.TypeReg && h.Name == entryName {
				return true
			}
		}
		return false

	default:
		zr, err := zip.OpenReader(archivePath)
		if err != nil {
			return false
		}
		defer zr.Close()
		for _, f := range zr.File {
			if f.Name == entryName {
				return true
			}
		}
		return false
	}
}

func ReadArchiveEntry(archivePath, entryName string) ([]byte, error) {
	var buf bytes.Buffer
	if !ServeArchiveEntry(&buf, func(string, int64) {}, archivePath, entryName) {
		return nil, fmt.Errorf("archive entry not found: %s!%s", archivePath, entryName)
	}
	return buf.Bytes(), nil
}

func ServeArchiveEntry(w io.Writer, setHeaders func(name string, size int64), archivePath, entryName string) bool {
	switch kindOf(archivePath) {
	case archivePdf:
		return false

	case archiveRar:
		rr, err := rardecode.OpenReader(archivePath)
		if err != nil {
			return false
		}
		defer rr.Close()

		for {
			h, err := rr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return false
			}
			if h.IsDir || h.Name != entryName {
				continue
			}
			setHeaders(h.Name, h.UnPackedSize)
			_, _ = io.Copy(w, rr)
			return true
		}
		return false

	case archiveSevenZip:
		zr, err := sevenzip.OpenReader(archivePath)
		if err != nil {
			return false
		}
		defer zr.Close()

		for _, f := range zr.File {
			if f.Name != entryName {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return false
			}
			defer rc.Close()
			setHeaders(f.Name, int64(f.UncompressedSize))
			_, _ = io.Copy(w, rc)
			return true
		}
		return false

	case archiveTar:
		f, err := os.Open(archivePath)
		if err != nil {
			return false
		}
		defer f.Close()

		tr := tar.NewReader(f)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return false
			}
			if h.Typeflag != tar.TypeReg || h.Name != entryName {
				continue
			}
			setHeaders(h.Name, h.Size)
			_, _ = io.Copy(w, tr)
			return true
		}
		return false

	default:
		zr, err := zip.OpenReader(archivePath)
		if err != nil {
			return false
		}
		defer zr.Close()

		for _, f := range zr.File {
			if f.Name != entryName {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return false
			}
			defer rc.Close()
			setHeaders(f.Name, int64(f.UncompressedSize64))
			_, _ = io.Copy(w, rc)
			return true
		}
		return false
	}
}

func listArchiveImageNames(archivePath string) ([]string, error) {
	switch kindOf(archivePath) {
	case archivePdf:
		return pdfPageNames(archivePath)

	case archiveRar:
		rr, err := rardecode.OpenReader(archivePath)
		if err != nil {
			return nil, err
		}
		defer rr.Close()

		var names []string
		for {
			h, err := rr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			if h.IsDir {
				continue
			}
			if !imageExts[strings.ToLower(filepath.Ext(h.Name))] {
				continue
			}
			names = append(names, h.Name)
		}
		return names, nil

	case archiveSevenZip:
		zr, err := sevenzip.OpenReader(archivePath)
		if err != nil {
			return nil, err
		}
		defer zr.Close()

		names := make([]string, 0, len(zr.File))
		for _, f := range zr.File {
			if f.FileInfo().IsDir() {
				continue
			}
			if !imageExts[strings.ToLower(filepath.Ext(f.Name))] {
				continue
			}
			names = append(names, f.Name)
		}
		return names, nil

	case archiveTar:
		f, err := os.Open(archivePath)
		if err != nil {
			return nil, err
		}
		defer f.Close()

		var names []string
		tr := tar.NewReader(f)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			if h.Typeflag != tar.TypeReg {
				continue
			}
			if !imageExts[strings.ToLower(filepath.Ext(h.Name))] {
				continue
			}
			names = append(names, h.Name)
		}
		return names, nil

	default:
		zr, err := zip.OpenReader(archivePath)
		if err != nil {
			return nil, err
		}
		defer zr.Close()

		names := make([]string, 0, len(zr.File))
		for _, f := range zr.File {
			if f.FileInfo().IsDir() {
				continue
			}
			if !imageExts[strings.ToLower(filepath.Ext(f.Name))] {
				continue
			}
			names = append(names, f.Name)
		}
		return names, nil
	}
}
