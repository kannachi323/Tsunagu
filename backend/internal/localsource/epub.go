package localsource

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"
)

var errNoOpf = errors.New("epub: no OPF package document found")

type EpubChapter struct {
	Title     string
	EntryPath string
}

type epubContainer struct {
	Rootfiles []struct {
		FullPath string `xml:"full-path,attr"`
	} `xml:"rootfiles>rootfile"`
}

type epubPackage struct {
	Manifest struct {
		Items []struct {
			ID   string `xml:"id,attr"`
			Href string `xml:"href,attr"`
		} `xml:"item"`
	} `xml:"manifest"`
	Spine struct {
		ItemRefs []struct {
			IDRef string `xml:"idref,attr"`
		} `xml:"itemref"`
	} `xml:"spine"`
}

func ParseEpubSpine(epubPath string) ([]EpubChapter, error) {
	zr, err := zip.OpenReader(epubPath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	files := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		files[f.Name] = f
	}

	containerData, err := readZipEntry(files["META-INF/container.xml"])
	if err != nil {
		return nil, err
	}
	var container epubContainer
	if err := xml.Unmarshal(containerData, &container); err != nil {
		return nil, err
	}
	if len(container.Rootfiles) == 0 {
		return nil, errNoOpf
	}
	opfPath := container.Rootfiles[0].FullPath
	opfDir := path.Dir(opfPath)

	opfData, err := readZipEntry(files[opfPath])
	if err != nil {
		return nil, err
	}
	var pkg epubPackage
	if err := xml.Unmarshal(opfData, &pkg); err != nil {
		return nil, err
	}

	hrefByID := make(map[string]string, len(pkg.Manifest.Items))
	for _, item := range pkg.Manifest.Items {
		hrefByID[item.ID] = item.Href
	}

	chapters := make([]EpubChapter, 0, len(pkg.Spine.ItemRefs))
	for i, ref := range pkg.Spine.ItemRefs {
		href, ok := hrefByID[ref.IDRef]
		if !ok {
			continue
		}
		entryPath := href
		if opfDir != "." {
			entryPath = path.Join(opfDir, href)
		}
		if _, ok := files[entryPath]; !ok {
			continue
		}
		chapters = append(chapters, EpubChapter{
			Title:     spineChapterTitle(files[entryPath], i+1),
			EntryPath: entryPath,
		})
	}
	return chapters, nil
}

func readZipEntry(f *zip.File) ([]byte, error) {
	if f == nil {
		return nil, errNoOpf
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

var titleTagRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

func spineChapterTitle(f *zip.File, fallbackNum int) string {
	data, err := readZipEntry(f)
	if err == nil {
		if m := titleTagRe.FindSubmatch(data); m != nil {
			if t := strings.TrimSpace(string(m[1])); t != "" {
				return t
			}
		}
	}
	return "Chapter " + strconv.Itoa(fallbackNum)
}
