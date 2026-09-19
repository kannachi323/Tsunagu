package localsource

import "strings"

// A CBZ/ZIP page's local_path is a virtual "zip://<archive path>!<entry name>"
// URL rather than a real filesystem path, so the same local_path column can
// point either at a loose image file or at an entry inside an archive.
const zipPrefix = "zip://"

func ZipPagePath(archivePath, entryName string) string {
	return zipPrefix + archivePath + "!" + entryName
}

func ParseZipPagePath(p string) (archivePath, entryName string, ok bool) {
	rest, found := strings.CutPrefix(p, zipPrefix)
	if !found {
		return "", "", false
	}
	i := strings.LastIndex(rest, "!")
	if i < 0 {
		return "", "", false
	}
	return rest[:i], rest[i+1:], true
}

// A DOCX book's local_path is a virtual "docx://<path>" URL — the whole
// document is converted to HTML on read, there's no archive entry to name.
const docxPrefix = "docx://"

func DocxBookPath(docxPath string) string {
	return docxPrefix + docxPath
}

func ParseDocxBookPath(p string) (docxPath string, ok bool) {
	rest, found := strings.CutPrefix(p, docxPrefix)
	return rest, found
}
